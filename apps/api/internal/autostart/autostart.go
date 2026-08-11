// Package autostart starts servers whose persisted auto_start setting is
// enabled when the panel API boots.
package autostart

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/store"
)

type serverStore interface {
	ListServers(context.Context) ([]*store.Server, error)
	GetNode(context.Context, string) (*store.Node, error)
	UpdateServerStatus(context.Context, string, string) error
	InsertLogEvent(context.Context, string, string, string, string) error
	LogAction(context.Context, string, string, string, string, any) error
}

type agentClient interface {
	GetStatus(context.Context, string) (map[string]any, error)
	StartServer(context.Context, string, map[string]any) error
}

type clientFactory func(*store.Node) agentClient

type runConfig struct {
	availabilityWindow time.Duration
	retryDelay         time.Duration
	statusTimeout      time.Duration
	startTimeout       time.Duration
}

var productionConfig = runConfig{
	availabilityWindow: 2 * time.Minute,
	retryDelay:         2 * time.Second,
	statusTimeout:      10 * time.Second,
	startTimeout:       16 * time.Minute,
}

// Run starts all opted-in servers and returns after every startup attempt has
// either been handed to an agent or exhausted the agent-availability window.
func Run(ctx context.Context, s *store.Store) {
	run(ctx, s, func(node *store.Node) agentClient {
		return agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
	}, productionConfig)
}

func run(ctx context.Context, s serverStore, factory clientFactory, cfg runConfig) {
	servers, err := s.ListServers(ctx)
	if err != nil {
		slog.Error("auto-start could not list servers", "error", err)
		return
	}

	var wg sync.WaitGroup
	for _, srv := range servers {
		if !srv.AutoStart {
			continue
		}
		wg.Add(1)
		go func(srv *store.Server) {
			defer wg.Done()
			startOne(ctx, s, factory, cfg, srv)
		}(srv)
	}
	wg.Wait()
}

func startOne(ctx context.Context, s serverStore, factory clientFactory, cfg runConfig, srv *store.Server) {
	node, err := s.GetNode(ctx, srv.NodeID)
	if err != nil {
		recordFailure(ctx, s, srv, fmt.Errorf("node lookup: %w", err))
		return
	}
	client := factory(node)
	availabilityCtx, cancelAvailability := context.WithTimeout(ctx, cfg.availabilityWindow)
	defer cancelAvailability()

	for {
		statusCtx, cancelStatus := context.WithTimeout(availabilityCtx, cfg.statusTimeout)
		state, statusErr := client.GetStatus(statusCtx, srv.ID)
		cancelStatus()
		if statusErr == nil {
			status, _ := state["status"].(string)
			switch status {
			case "online", "starting", "stopping":
				if err := s.UpdateServerStatus(ctx, srv.ID, status); err != nil {
					slog.Error("auto-start status sync failed", "server_id", srv.ID, "status", status, "error", err)
				}
				return
			default:
				startCtx, cancelStart := context.WithTimeout(ctx, cfg.startTimeout)
				if err := s.UpdateServerStatus(ctx, srv.ID, "starting"); err != nil {
					slog.Error("auto-start status update failed", "server_id", srv.ID, "error", err)
				}
				startErr := client.StartServer(startCtx, srv.ID, agent.StartConfigForServer(srv))
				cancelStart()
				if startErr != nil {
					_ = s.UpdateServerStatus(ctx, srv.ID, "offline")
					recordFailure(ctx, s, srv, startErr)
					return
				}
				_ = s.LogAction(ctx, "", srv.ID, "server.autostart", "", nil)
				slog.Info("auto-start handed server to agent", "server_id", srv.ID, "server_name", srv.Name)
				return
			}
		}

		timer := time.NewTimer(cfg.retryDelay)
		select {
		case <-availabilityCtx.Done():
			timer.Stop()
			if ctx.Err() == nil {
				recordFailure(ctx, s, srv, fmt.Errorf("agent unavailable for %s: %w", cfg.availabilityWindow, statusErr))
			}
			return
		case <-timer.C:
		}
	}
}

func recordFailure(ctx context.Context, s serverStore, srv *store.Server, err error) {
	slog.Error("server auto-start failed", "server_id", srv.ID, "server_name", srv.Name, "error", err)
	_ = s.LogAction(ctx, "", srv.ID, "server.autostart_failed", "", map[string]any{"error": err.Error()})
	_ = s.InsertLogEvent(ctx, srv.ID, "error", "Automatic start failed: "+err.Error(), "api")
}
