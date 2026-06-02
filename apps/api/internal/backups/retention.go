// Package backups holds backup lifecycle helpers shared by the HTTP handlers
// and the scheduler — notably retention enforcement, which prunes old backup
// zips on the agent and their DB rows.
package backups

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/store"
)

// Policy is the retention shape stored as JSON on backup_targets.retention.
type Policy struct {
	KeepLastN  int `json:"keep_last_n"`
	MaxAgeDays int `json:"max_age_days"`
}

// DefaultKeepLastN applies when a server has no backup target / retention set,
// so backups don't accumulate without bound.
const DefaultKeepLastN = 10

// Enforce prunes successful backups for a server beyond its retention policy.
// It is best-effort: agent/file failures are logged, not fatal, and the DB row
// is only removed once the agent confirms (or the zip is already gone).
func Enforce(ctx context.Context, s *store.Store, serverID string) {
	policy := resolvePolicy(ctx, s, serverID)

	all, err := s.ListBackups(ctx, serverID)
	if err != nil {
		log.Printf("retention: list backups %s: %v", serverID, err)
		return
	}

	// Only successful backups are candidates; ListBackups is newest-first.
	var keep []*store.Backup
	for _, b := range all {
		if b.Status == "success" {
			keep = append(keep, b)
		}
	}

	var toDelete []*store.Backup
	if policy.KeepLastN > 0 && len(keep) > policy.KeepLastN {
		toDelete = append(toDelete, keep[policy.KeepLastN:]...)
	}
	if policy.MaxAgeDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -policy.MaxAgeDays)
		for _, b := range keep {
			if b.StartedAt.Before(cutoff) {
				toDelete = append(toDelete, b)
			}
		}
	}
	if len(toDelete) == 0 {
		return
	}

	srv, err := s.GetServer(ctx, serverID)
	if err != nil {
		log.Printf("retention: server %s: %v", serverID, err)
		return
	}
	node, err := s.GetNode(ctx, srv.NodeID)
	if err != nil {
		log.Printf("retention: node for %s: %v", serverID, err)
		return
	}
	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
	if err := c.RegisterDir(ctx, serverID, srv.DirectoryPath); err != nil {
		log.Printf("retention: register dir %s: %v", serverID, err)
		return
	}

	seen := map[string]bool{}
	for _, b := range toDelete {
		if seen[b.ID] {
			continue
		}
		seen[b.ID] = true
		if err := c.DeleteBackup(ctx, serverID, b.ID); err != nil {
			log.Printf("retention: delete zip %s: %v", b.ID, err)
			continue
		}
		if err := s.DeleteBackup(ctx, b.ID); err != nil {
			log.Printf("retention: delete row %s: %v", b.ID, err)
		}
	}
}

func resolvePolicy(ctx context.Context, s *store.Store, serverID string) Policy {
	policy := Policy{KeepLastN: DefaultKeepLastN}
	targets, err := s.ListBackupTargets(ctx, serverID)
	if err != nil {
		return policy
	}
	for _, t := range targets {
		if len(t.Retention) == 0 {
			continue
		}
		var p Policy
		if err := json.Unmarshal(t.Retention, &p); err != nil {
			continue
		}
		if t.IsDefault || p.KeepLastN > 0 || p.MaxAgeDays > 0 {
			return p
		}
	}
	return policy
}
