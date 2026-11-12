// Package dbbackup keeps recent copies of the panel's own database.
//
// The panel backs up every server's world but, before this, nothing backed up
// the panel: one bad disk block or a mistaken delete took users, permissions,
// schedules, audit history and every node's token with it. A copy is taken on
// an interval with SQLite's VACUUM INTO, which writes a consistent,
// defragmented snapshot while the API keeps serving — no stop, no WAL to copy
// alongside.
//
// The copies sit on the same disk by default, which covers a damaged or
// mis-migrated database but not a lost disk; point DB_BACKUP_DIR at other
// storage, or sync the directory off the host, for that. A copy is only whole
// together with the app encryption key (see the README's backup section): the
// key is deliberately not copied here, so the directory can be shipped
// somewhere less trusted than the host.
package dbbackup

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	prefix     = "mcsm-"
	suffix     = ".db"
	timeLayout = "20060102T150405Z"

	// startupDelay keeps an overdue copy from competing with boot, the busiest
	// the database gets.
	startupDelay = 2 * time.Minute
	// retryDelay is how soon a failed copy is tried again.
	retryDelay = 30 * time.Minute
)

// Config says where copies go, how often one is taken, and how many are kept.
type Config struct {
	Dir      string
	Interval time.Duration
	Keep     int
}

// Enabled reports whether the configuration asks for copies at all.
func (c Config) Enabled() bool { return c.Dir != "" && c.Interval > 0 && c.Keep > 0 }

// Run takes copies on cfg's interval until ctx is cancelled. The schedule
// follows the newest copy on disk rather than the process's uptime, so a panel
// that restarts often neither skips its copies nor takes one per restart.
func Run(ctx context.Context, db *sql.DB, cfg Config) {
	if !cfg.Enabled() {
		return
	}
	timer := time.NewTimer(nextDelay(cfg, time.Now()))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		path, err := Snapshot(ctx, db, cfg.Dir, time.Now())
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("database backup failed", "dir", cfg.Dir, "error", err)
			timer.Reset(retryDelay)
			continue
		}
		slog.Info("database backup written", "path", path)
		if removed, err := Prune(cfg.Dir, cfg.Keep); err != nil {
			slog.Warn("database backup: removing old copies failed", "error", err)
		} else if removed > 0 {
			slog.Info("database backup: removed old copies", "count", removed)
		}
		timer.Reset(cfg.Interval)
	}
}

// nextDelay is how long to wait before the next copy: the rest of the interval
// since the newest one, and never less than startupDelay.
func nextDelay(cfg Config, now time.Time) time.Duration {
	newest, ok := newestCopy(cfg.Dir)
	if !ok {
		return startupDelay
	}
	return max(startupDelay, cfg.Interval-now.Sub(newest))
}

// Snapshot writes one consistent copy of the database into dir and returns its
// path. The copy appears under its final name only once it is complete.
func Snapshot(ctx context.Context, db *sql.DB, dir string, now time.Time) (string, error) {
	// The directory holds everything the database does; keep it the API's alone.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	final := filepath.Join(dir, prefix+now.UTC().Format(timeLayout)+suffix)
	part := final + ".partial"
	// VACUUM INTO refuses an existing file; a leftover from a crash goes first.
	if err := os.Remove(part); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", part); err != nil {
		_ = os.Remove(part)
		return "", fmt.Errorf("vacuum into %s: %w", part, err)
	}
	_ = os.Chmod(part, 0o600)
	if err := os.Rename(part, final); err != nil {
		_ = os.Remove(part)
		return "", err
	}
	return final, nil
}

// Prune removes all but the newest keep copies and reports how many went.
func Prune(dir string, keep int) (int, error) {
	names, err := copies(dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for i := 0; i < len(names)-keep; i++ {
		if err := os.Remove(filepath.Join(dir, names[i])); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// copies lists the finished copies in dir, oldest first. The names carry their
// time in a fixed-width form, so sorting them sorts them by age.
func copies(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if _, ok := copyTime(e.Name()); ok && e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func newestCopy(dir string) (time.Time, bool) {
	names, err := copies(dir)
	if err != nil || len(names) == 0 {
		return time.Time{}, false
	}
	return copyTime(names[len(names)-1])
}

// copyTime reads the time out of a copy's name; anything else in the directory
// is not ours and is left alone.
func copyTime(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return time.Time{}, false
	}
	t, err := time.Parse(timeLayout, strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix))
	return t, err == nil
}
