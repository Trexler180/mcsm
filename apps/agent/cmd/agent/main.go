package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	agentapi "github.com/mcsm/agent/internal/api"
	"github.com/mcsm/agent/internal/metrics"
	"github.com/mcsm/agent/internal/process"
)

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

	mgr := process.NewManager()
	collector := metrics.NewCollector()
	serverRoot := defaultServerRoot()

	router := agentapi.NewRouter(token, mgr, collector, serverRoot)

	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%s", host, port),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // streaming responses need no write timeout
		IdleTimeout:  120 * time.Second,
	}

	certFile := os.Getenv("AGENT_TLS_CERT")
	keyFile := os.Getenv("AGENT_TLS_KEY")

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

	// Gracefully stop running MC children so they aren't orphaned (P2 #18).
	mgr.StopAll(30 * time.Second)

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
