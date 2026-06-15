package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	panelapi "github.com/mcsm/api/internal/api"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/autoupdate"
	"github.com/mcsm/api/internal/poller"
	"github.com/mcsm/api/internal/scheduler"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

// setupLogging configures slog as the process logger and routes the stdlib log
// package through it so existing log.Printf calls share one structured stream.
// LOG_FORMAT=json|text (default text), LOG_LEVEL=debug|info|warn|error.
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
	logger := slog.New(h)
	slog.SetDefault(logger)

	// Bridge stdlib log → slog so log.Printf / log.Fatalf are structured too.
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

	dbPath := envOr("DATABASE_PATH", "mcsm.db")
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		if !isDevMode() {
			log.Fatal("JWT_SECRET is required outside development mode")
		}
		var err error
		jwtSecret, err = randomHex(32)
		if err != nil {
			log.Fatalf("generate jwt secret: %v", err)
		}
		log.Println("JWT_SECRET is not set; using an ephemeral signing key. Existing sessions will be invalid after restart.")
	}
	port := envOr("API_PORT", "8081")
	host := envOr("API_HOST", "127.0.0.1")
	serverRoot := defaultServerRoot()
	if err := os.MkdirAll(serverRoot, 0755); err != nil {
		log.Fatalf("create server root: %v", err)
	}
	if err := ensureWritableDir(serverRoot); err != nil {
		log.Fatalf("server root is not writable: %v", err)
	}
	if err := ensureWritableDir(filepath.Join(serverRoot, "mcsm-backups")); err != nil {
		log.Fatalf("backup root is not writable: %v", err)
	}

	adminEmail := envOr("ADMIN_EMAIL", "admin@example.com")
	adminPassword := os.Getenv("ADMIN_PASSWORD")

	ctx := context.Background()

	// Ensure parent directory exists
	if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Fatalf("create db dir: %v", err)
		}
	}

	// Open SQLite with sensible pragmas:
	//  - foreign_keys: enforce FK constraints (off by default in SQLite)
	//  - journal_mode=WAL: concurrent reads during writes
	//  - busy_timeout: wait up to 5s on a locked DB before erroring
	//  - synchronous=NORMAL: the SQLite-recommended pairing with WAL. Only
	//    fsyncs at checkpoint instead of every commit, which is markedly
	//    faster for writes and cannot corrupt the DB (a crash may at most
	//    lose the last committed transaction, not integrity).
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)",
		url.PathEscape(dbPath))

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		log.Fatalf("db open: %v", err)
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("db ping: %v", err)
	}
	log.Printf("opened sqlite database at %s", dbPath)

	// Run migrations
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		log.Fatalf("goose dialect: %v", err)
	}
	if err := goose.Up(db, "."); err != nil {
		log.Fatalf("migrations: %v", err)
	}
	log.Println("migrations applied")

	s := store.New(db).WithEncryption(resolveMasterKey(dbPath, jwtSecret))

	// Seed admin user on first boot
	if n, _ := s.CountUsers(ctx); n == 0 {
		generatedPassword := false
		if adminPassword == "" {
			var err error
			adminPassword, err = randomHex(12)
			if err != nil {
				log.Fatalf("generate admin password: %v", err)
			}
			generatedPassword = true
		}
		hash, err := auth.HashPassword(adminPassword)
		if err != nil {
			log.Fatalf("hash password: %v", err)
		}
		u, err := s.CreateUser(ctx, adminEmail, hash, "admin")
		if err != nil {
			log.Fatalf("create admin: %v", err)
		}
		log.Printf("created admin user: %s", u.Email)
		if generatedPassword {
			log.Printf("generated initial admin password for %s: %s", u.Email, adminPassword)
		}
	} else if os.Getenv("RESET_ADMIN_PASSWORD") == "1" {
		if adminPassword == "" {
			log.Fatal("ADMIN_PASSWORD is required when RESET_ADMIN_PASSWORD=1")
		}
		hash, err := auth.HashPassword(adminPassword)
		if err != nil {
			log.Fatalf("hash password: %v", err)
		}
		u, err := s.EnsureAdminUser(ctx, adminEmail, hash)
		if err != nil {
			log.Fatalf("reset admin password: %v", err)
		}
		log.Printf("reset admin user password: %s", u.Email)
	}

	if os.Getenv("AUTO_REGISTER_LOCAL_AGENT") == "1" {
		name := envOr("LOCAL_AGENT_NAME", "Local Agent")
		fqdn := envOr("LOCAL_AGENT_FQDN", "localhost")
		scheme := envOr("LOCAL_AGENT_SCHEME", "http")
		token := os.Getenv("LOCAL_AGENT_TOKEN")
		port, err := strconv.Atoi(envOr("LOCAL_AGENT_PORT", "8090"))
		if err != nil {
			log.Fatalf("LOCAL_AGENT_PORT must be a number: %v", err)
		}
		if token == "" {
			log.Fatal("LOCAL_AGENT_TOKEN is required when AUTO_REGISTER_LOCAL_AGENT=1")
		}
		if token == "dev-agent-token" && !isDevMode() {
			log.Fatal("LOCAL_AGENT_TOKEN must not use the default dev token outside development mode")
		}
		n, err := s.EnsureNode(ctx, name, fqdn, port, scheme, token)
		if err != nil {
			log.Fatalf("register local agent node: %v", err)
		}
		log.Printf("registered local agent node: %s (%s://%s:%d)", n.Name, n.Scheme, n.FQDN, n.Port)
	}

	updater := autoupdate.New(s)
	router := panelapi.NewRouter(s, jwtSecret, serverRoot, updater)

	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%s", host, port),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	// Background workers: cron-driven scheduled tasks + status synchronization
	bgCtx, bgCancel := context.WithCancel(context.Background())

	sched := scheduler.New(s, updater)
	sched.Start(bgCtx)

	go poller.Run(bgCtx, s)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("api listening on %s:%s", host, port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	<-stop
	log.Println("shutting down api...")

	bgCancel()
	sched.Stop()

	shutCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	srv.Shutdown(shutCtx)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func defaultServerRoot() string {
	if v := os.Getenv("SERVER_ROOT"); v != "" {
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

// resolveMasterKey returns the master secret used to encrypt app secrets at
// rest, chosen so stored keys survive restarts without re-entry. Precedence:
//
//  1. APP_ENCRYPTION_KEY — explicit, strongest: the key lives only in the
//     environment, never on disk, and is unaffected by anything else.
//  2. A persistent key auto-generated once and stored beside the database
//     (override the path with APP_ENCRYPTION_KEY_FILE). This is the zero-config
//     default: it survives restarts and JWT_SECRET rotation, so a key pasted in
//     Settings stays valid. At-rest protection here is only as strong as the
//     file permissions on the key file.
//  3. The JWT secret — last-resort fallback only if the key file can't be read
//     or written. If that secret is ephemeral (dev mode, no JWT_SECRET set),
//     stored secrets won't survive a restart; the log line says so.
func resolveMasterKey(dbPath, jwtSecret string) string {
	if k := os.Getenv("APP_ENCRYPTION_KEY"); k != "" {
		return k
	}

	keyPath := os.Getenv("APP_ENCRYPTION_KEY_FILE")
	if keyPath == "" {
		keyPath = filepath.Join(filepath.Dir(dbPath), ".mcsm-secret-key")
	}
	if b, err := os.ReadFile(keyPath); err == nil {
		if k := strings.TrimSpace(string(b)); k != "" {
			return k
		}
	}

	k, err := randomHex(32)
	if err != nil {
		log.Printf("could not generate app encryption key (%v); falling back to JWT secret", err)
		return jwtSecret
	}
	if err := os.WriteFile(keyPath, []byte(k), 0600); err != nil {
		log.Printf("could not persist app encryption key at %s (%v); stored secrets will not survive restart unless APP_ENCRYPTION_KEY is set", keyPath, err)
		return jwtSecret
	}
	log.Printf("generated persistent app encryption key at %s", keyPath)
	return k
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
