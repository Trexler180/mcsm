package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	panelapi "github.com/mcsm/api/internal/api"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/poller"
	"github.com/mcsm/api/internal/scheduler"
	"github.com/mcsm/api/internal/store"
	"github.com/mcsm/api/migrations"
)

func main() {
	dbPath := envOr("DATABASE_PATH", "mcsm.db")
	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		var err error
		jwtSecret, err = randomHex(32)
		if err != nil {
			log.Fatalf("generate jwt secret: %v", err)
		}
		log.Println("JWT_SECRET is not set; using an ephemeral signing key. Existing sessions will be invalid after restart.")
	}
	port := envOr("API_PORT", "8080")
	host := envOr("API_HOST", "127.0.0.1")
	serverRoot := defaultServerRoot()
	if err := os.MkdirAll(serverRoot, 0755); err != nil {
		log.Fatalf("create server root: %v", err)
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
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
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

	s := store.New(db)

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
		n, err := s.EnsureNode(ctx, name, fqdn, port, scheme, token)
		if err != nil {
			log.Fatalf("register local agent node: %v", err)
		}
		log.Printf("registered local agent node: %s (%s://%s:%d)", n.Name, n.Scheme, n.FQDN, n.Port)
	}

	router := panelapi.NewRouter(s, jwtSecret, serverRoot)

	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%s", host, port),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	// Background workers: cron-driven scheduled tasks + status synchronization
	bgCtx, bgCancel := context.WithCancel(context.Background())

	sched := scheduler.New(s)
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
