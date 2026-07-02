package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ── Nodes ────────────────────────────────────────────────────────

func (s *Store) CreateNode(ctx context.Context, n *Node, token string) (*Node, error) {
	id := uuid.NewString()
	encToken, err := s.EncryptNodeToken(token)
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO nodes (id, name, fqdn, port, scheme, token, location)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, n.Name, n.FQDN, n.Port, n.Scheme, encToken, n.Location,
	)
	if err != nil {
		return nil, err
	}
	return s.GetNode(ctx, id)
}

func (s *Store) EnsureNode(ctx context.Context, name, fqdn string, port int, scheme, token string) (*Node, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM nodes WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return s.CreateNode(ctx, &Node{
			Name:   name,
			FQDN:   fqdn,
			Port:   port,
			Scheme: scheme,
		}, token)
	}
	if err != nil {
		return nil, err
	}
	encToken, err := s.EncryptNodeToken(token)
	if err != nil {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE nodes SET fqdn=?, port=?, scheme=?, token=? WHERE id=?`,
		fqdn, port, scheme, encToken, id,
	)
	if err != nil {
		return nil, err
	}
	return s.GetNode(ctx, id)
}

func (s *Store) GetNode(ctx context.Context, id string) (*Node, error) {
	var n Node
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, fqdn, port, scheme, token, memory_mb, disk_gb, cpu_cores, location, created_at, last_seen FROM nodes WHERE id = ?`,
		id,
	).Scan(&n.ID, &n.Name, &n.FQDN, &n.Port, &n.Scheme, &n.Token, &n.MemoryMb, &n.DiskGb, &n.CPUCores, &n.Location, &n.CreatedAt, &n.LastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("node not found")
	}
	if err != nil {
		return nil, err
	}
	if n.Token, err = s.DecryptNodeToken(n.Token); err != nil {
		return nil, err
	}
	return &n, nil
}

func (s *Store) ListNodes(ctx context.Context) ([]*Node, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, fqdn, port, scheme, token, memory_mb, disk_gb, cpu_cores, location, created_at, last_seen FROM nodes ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var nodes []*Node
	for rows.Next() {
		var n Node
		if err := rows.Scan(&n.ID, &n.Name, &n.FQDN, &n.Port, &n.Scheme, &n.Token, &n.MemoryMb, &n.DiskGb, &n.CPUCores, &n.Location, &n.CreatedAt, &n.LastSeen); err != nil {
			return nil, err
		}
		token, err := s.DecryptNodeToken(n.Token)
		if err != nil {
			return nil, err
		}
		n.Token = token
		nodes = append(nodes, &n)
	}
	return nodes, rows.Err()
}

func (s *Store) UpdateNode(ctx context.Context, id string, n *Node) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET name=?, fqdn=?, port=?, scheme=?, location=? WHERE id=?`,
		n.Name, n.FQDN, n.Port, n.Scheme, n.Location, id,
	)
	return err
}

func (s *Store) DeleteNode(ctx context.Context, id string) error {
	n, err := s.CountServersForNode(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrNodeHasServers
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM nodes WHERE id = ?`, id)
	return err
}

func (s *Store) UpdateNodeSeen(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE nodes SET last_seen = CURRENT_TIMESTAMP WHERE id = ?`, id)
	return err
}

func (s *Store) UpdateNodeHeartbeat(ctx context.Context, id string, memoryMb, diskGb, cpuCores *int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE nodes SET memory_mb=?, disk_gb=?, cpu_cores=?, last_seen=CURRENT_TIMESTAMP WHERE id=?`,
		memoryMb, diskGb, cpuCores, id,
	)
	return err
}
