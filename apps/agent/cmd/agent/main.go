package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	agentapi "github.com/mcsm/agent/internal/api"
	"github.com/mcsm/agent/internal/metrics"
	"github.com/mcsm/agent/internal/process"
)

func main() {
	token := os.Getenv("AGENT_TOKEN")
	if token == "" {
		log.Fatal("AGENT_TOKEN environment variable is required")
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
	if err := os.MkdirAll(serverRoot, 0755); err != nil {
		log.Fatalf("create server root: %v", err)
	}

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

	<-stop
	log.Println("shutting down agent...")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
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
