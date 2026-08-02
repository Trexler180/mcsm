package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	agentapi "github.com/mcsm/agent/internal/api"
	"github.com/mcsm/agent/internal/api/handlers"
	"github.com/mcsm/agent/internal/link"
	"github.com/mcsm/agent/internal/metrics"
	"github.com/mcsm/agent/internal/process"
)

// version can be pinned with -ldflags "-X main.version=..."; when left empty,
// resolveVersion falls back to the git revision Go embeds in the binary.
var version = ""

// resolveVersion returns the build identity: the explicit -X value, else the
// VCS revision baked in by `go build` from a git checkout ("-dirty" when the
// tree had uncommitted changes), else "dev".
func resolveVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		var rev, dirty string
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				if s.Value == "true" {
					dirty = "-dirty"
				}
			}
		}
		if rev != "" {
			if len(rev) > 12 {
				rev = rev[:12]
			}
			return rev + dirty
		}
	}
	return "dev"
}

// setupLogging mirrors the API: slog as default, stdlib log bridged through it.
func setupLogging() {
	var level slog.Level
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(os.Getenv("LOG_FORMAT"), "json") {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))
	log.SetFlags(0)
	log.SetOutput(slogWriter{})
}

type slogWriter struct{}

func (slogWriter) Write(p []byte) (int, error) {
	slog.Info(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func main() {
	setupLogging()

	handlers.Version = resolveVersion()
	log.Printf("mcsm-agent %s starting", handlers.Version)

	token := os.Getenv("AGENT_TOKEN")
	if token == "" {
		log.Fatal("AGENT_TOKEN environment variable is required")
	}
	if token == "dev-agent-token" && !isDevMode() {
		log.Fatal("AGENT_TOKEN must not use the default dev token outside development mode")
	}

	port := os.Getenv("AGENT_PORT")
	if port == "" {
		port = "8090"
	}
	host := os.Getenv("AGENT_HOST")
	if host == "" {
		host = "127.0.0.1"
	}

	certFile := os.Getenv("AGENT_TLS_CERT")
	keyFile := os.Getenv("AGENT_TLS_KEY")
	tlsEnabled := certFile != "" && keyFile != ""

	// The agent is an RCE surface (it launches processes and reads/writes the
	// server filesystem) protected only by a bearer token. Binding it to a public
	// interface in plaintext would expose that token — and everything it guards —
	// to network sniffing. Refuse such a bind unless TLS is configured or the
	// operator explicitly accepts the risk (e.g. an already-encrypted overlay
	// network). Loopback binds and dev mode are always allowed.
	if !isLoopbackHost(host) && !tlsEnabled && os.Getenv("AGENT_ALLOW_INSECURE") != "1" && !isDevMode() {
		log.Fatalf("refusing to bind agent to non-loopback %q without TLS: set AGENT_TLS_CERT/AGENT_TLS_KEY, bind AGENT_HOST=127.0.0.1, or set AGENT_ALLOW_INSECURE=1 to override", host)
	}

	serverRoot := defaultServerRoot()
	mgr := process.NewManager(serverRoot)
	collector := metrics.NewCollector()

	// Adopt any Minecraft servers that kept running across a previous agent
	// restart or upgrade, so a deploy doesn't take servers down (P0).
	mgr.Reattach()

	// Servers dial the agent back on this host — the JVM is always local, so
	// loopback is preferred: it keeps the credential off the network entirely.
	// But loopback only reaches a listener that is actually bound to it, so when
	// the agent is pinned to a single non-loopback interface the spawned servers
	// must dial that address instead. (A wildcard bind fails net.ParseIP's
	// loopback check but does include loopback, so it still gets 127.0.0.1.)
	linkHost := "127.0.0.1"
	if host != "" && host != "0.0.0.0" && host != "::" && !isLoopbackHost(host) {
		linkHost = host
	}
	linkScheme := "http"
	if tlsEnabled {
		// The mod upgrades http→ws and https→wss; a TLS-only listener must be
		// advertised as such or the handshake dies at the first byte.
		linkScheme = "https"
		// ...and it must be advertised under a name the certificate covers, or the
		// mod's own hostname verification rejects a link it would otherwise make.
		resolved, reason := linkHostForTLS(certFile, linkHost)
		if reason != "" {
			log.Printf("helper mod link over TLS: %s", reason)
		}
		linkHost = resolved
	}
	if linkHost == "" {
		// No advertisable host. linkEnviron treats an empty URL as "stay dormant",
		// which leaves servers on the log-scraping and stdin paths rather than
		// having every one of them retry a handshake that cannot succeed.
		process.LinkAgentURL = ""
	} else {
		process.LinkAgentURL = fmt.Sprintf("%s://%s:%s", linkScheme, linkHost, port)
	}

	// Helper mod build resolution. The mod is released from its own repository,
	// so new Minecraft versions are supported by publishing a build there rather
	// than by shipping a new manager. With no index configured the agent uses
	// only the build embedded in this binary.
	helperResolver := process.EmbeddedOnlyResolver()
	helperResolver.IndexURL = os.Getenv("MCSM_HELPER_INDEX_URL")
	helperResolver.CacheDir = filepath.Join(serverRoot, ".mcsm-run", "helper-cache")
	process.HelperModResolver = helperResolver
	if helperResolver.IndexURL != "" {
		log.Printf("helper mod index: %s", helperResolver.IndexURL)
	}

	// Helper-mod link. Servers without the mod never touch this and keep using
	// the log-scraping and stdin paths.
	linkSink := link.NewMemorySink()

	// Feed the mod's authoritative player list into the roster the panel already
	// reads. This is what stops the players tab from typing `/list` into the
	// server: the mod becomes a better source for the same pipeline rather than
	// a second, parallel one the UI would have to choose between.
	linkSink.OnSnapshotFunc(func(serverID string, snap link.Snapshot) {
		players := make([]process.Player, 0, len(snap.Players.List))
		for _, p := range snap.Players.List {
			players = append(players, process.Player{
				Name:   p.Name,
				UUID:   p.UUID,
				Online: true,
			})
		}
		mgr.SetLinkRoster(serverID, players)
	})

	// The roster entry has a freshness TTL, but waiting it out means up to 45s
	// of a stopped server still "showing" players. A disconnect is a positive
	// signal that mod data is no longer live — act on it immediately and let the
	// console path take over.
	linkSink.OnDisconnectFunc(mgr.ClearLinkRoster)

	// A disconnect keeps the last snapshot on purpose; a purge means the server
	// is gone for good, so drop the entry rather than let it outlive the server.
	mgr.OnUnregisterFunc(linkSink.Forget)

	links := link.NewRegistry(linkSink, mgr)

	// Route console commands through the mod when one is linked. This is the only
	// production caller of the RPC path: player actions stay on stdin, because
	// nothing about touching the console should quietly reroute moderation.
	//
	// The error mapping is the load-bearing part. Only link.ErrNoSession and
	// link.ErrNotDelivered mean the command provably never reached the server, and
	// only those become ErrLinkUnavailable — the sentinel that authorises a stdin
	// retry. A timeout is passed through as an ordinary error precisely so it does
	// not earn one, since the command may already have run.
	mgr.SetLinkExec(func(ctx context.Context, serverID, cmd string) (process.ExecOutcome, error) {
		result, err := links.ExecCommand(ctx, serverID, cmd)
		switch {
		case err == nil:
			return process.ExecOutcome{ViaMod: true, Success: result.Success, Output: result.Output}, nil
		case errors.Is(err, link.ErrNoSession), errors.Is(err, link.ErrNotDelivered):
			return process.ExecOutcome{}, fmt.Errorf("%w: %v", process.ErrLinkUnavailable, err)
		default:
			return process.ExecOutcome{}, err
		}
	})

	router := agentapi.NewRouter(token, mgr, collector, serverRoot, links, linkSink)

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%s", host, port),
		Handler:           router,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second, // bound slow-header (slowloris) connections
		WriteTimeout:      0,                // streaming responses need no write timeout
		IdleTimeout:       120 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("agent listening on %s:%s", host, port)
		var err error
		if certFile != "" && keyFile != "" {
			err = srv.ListenAndServeTLS(certFile, keyFile)
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Prepare the server root off the bind path. Creating a dir and probing it
	// with a temp file is normally instant, but on a slow/removable disk — or
	// when antivirus scans the freshly-created probe file — it can stall for a
	// long time. Doing it synchronously before binding would (and did) leave the
	// agent started-but-not-listening, looking like a hang. Binding first keeps
	// the agent reachable regardless of disk latency; writability problems surface
	// as warnings here and as clear errors on the operations that need the disk.
	go ensureServerRoot(serverRoot)

	<-stop
	log.Println("shutting down agent...")

	// By default, leave Minecraft servers running across the restart so deploys
	// and agent upgrades don't take servers down — the next agent reattaches to
	// them on boot. Operators who want the old "stop everything with the agent"
	// behavior can opt in.
	if os.Getenv("AGENT_STOP_SERVERS_ON_EXIT") == "1" {
		log.Println("AGENT_STOP_SERVERS_ON_EXIT=1: stopping all servers")
		mgr.StopAll(30 * time.Second)
	} else {
		mgr.DetachAll()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}

// ensureServerRoot creates the server root and its backups dir and verifies they
// are writable. Run in the background after the agent is already listening, so a
// slow disk can't delay binding; failures are logged as warnings rather than
// fatal, since the per-request file operations create directories on demand and
// will report their own errors if the disk really is unusable.
func ensureServerRoot(serverRoot string) {
	if err := os.MkdirAll(serverRoot, 0755); err != nil {
		log.Printf("warning: create server root %q: %v", serverRoot, err)
		return
	}
	if err := ensureWritableDir(serverRoot); err != nil {
		log.Printf("warning: server root is not writable: %v", err)
	}
	if err := ensureWritableDir(filepath.Join(serverRoot, "mcsm-backups")); err != nil {
		log.Printf("warning: backup root is not writable: %v", err)
	}
}

func defaultServerRoot() string {
	if v := os.Getenv("AGENT_SERVER_ROOT"); v != "" {
		return v
	}
	if _, err := os.Stat("servers"); err == nil {
		return "servers"
	}
	return filepath.Join("..", "..", "servers")
}

func isDevMode() bool {
	v := strings.ToLower(os.Getenv("APP_ENV"))
	return os.Getenv("MCSM_DEV_MODE") == "1" || v == "dev" || v == "development" || v == "local"
}

// isLoopbackHost reports whether the bind host is the loopback interface, where
// the agent is only reachable from the same machine and plaintext is safe.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func ensureWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".mcsm-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}
