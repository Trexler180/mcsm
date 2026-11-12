package dbbackup

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// A copy holds what the live database held when it was taken, opens on its
// own, and is not disturbed by later writes.
func TestSnapshotIsAConsistentStandaloneCopy(t *testing.T) {
	dir := t.TempDir()
	db := openDB(t, filepath.Join(dir, "live.db"))
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE users (email TEXT); INSERT INTO users VALUES ('a@example.com')`); err != nil {
		t.Fatal(err)
	}

	backups := filepath.Join(dir, "db-backups")
	path, err := Snapshot(ctx, db, backups, time.Date(2026, 11, 10, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(backups, "mcsm-20261110T030405Z.db"); path != want {
		t.Fatalf("copy written to %s, want %s", path, want)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO users VALUES ('later@example.com')`); err != nil {
		t.Fatal(err)
	}

	copyDB := openDB(t, path)
	var n int
	if err := copyDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("copy holds %d users, want the 1 present when it was taken", n)
	}
	left, _ := filepath.Glob(filepath.Join(backups, "*.partial"))
	if len(left) != 0 {
		t.Fatalf("partial files left behind: %v", left)
	}
}

func touch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Pruning keeps the newest copies and never touches a file it did not write.
func TestPruneKeepsTheNewestAndLeavesOtherFilesAlone(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"mcsm-20261101T000000Z.db", "mcsm-20261102T000000Z.db",
		"mcsm-20261103T000000Z.db", "mcsm-20261104T000000Z.db",
		"mcsm-20261105T000000Z.db.partial", "notes.txt", "mcsm-latest.db",
	} {
		touch(t, dir, name)
	}
	removed, err := Prune(dir, 2)
	if err != nil || removed != 2 {
		t.Fatalf("removed %d, %v; want 2", removed, err)
	}
	for name, want := range map[string]bool{
		"mcsm-20261101T000000Z.db": false, "mcsm-20261102T000000Z.db": false,
		"mcsm-20261103T000000Z.db": true, "mcsm-20261104T000000Z.db": true,
		"mcsm-20261105T000000Z.db.partial": true, "notes.txt": true, "mcsm-latest.db": true,
	} {
		_, err := os.Stat(filepath.Join(dir, name))
		if (err == nil) != want {
			t.Errorf("%s: present=%v, want %v", name, err == nil, want)
		}
	}
}

// The schedule follows the newest copy on disk, so a restart neither skips a
// copy that is due nor takes one that is not.
func TestNextDelayFollowsTheNewestCopy(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Dir: dir, Interval: 24 * time.Hour, Keep: 7}
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

	if got := nextDelay(cfg, now); got != startupDelay {
		t.Fatalf("no copies yet: wait %v, want %v", got, startupDelay)
	}
	touch(t, dir, "mcsm-20261110T060000Z.db") // six hours old
	if got := nextDelay(cfg, now); got != 18*time.Hour {
		t.Fatalf("copy six hours old: wait %v, want 18h", got)
	}
	touch(t, dir, "mcsm-20261101T060000Z.db") // an older one changes nothing
	if got := nextDelay(cfg, now); got != 18*time.Hour {
		t.Fatalf("with an older copy too: wait %v, want 18h", got)
	}
	if got := nextDelay(cfg, now.Add(48*time.Hour)); got != startupDelay {
		t.Fatalf("overdue: wait %v, want %v", got, startupDelay)
	}
}

func TestConfigEnabled(t *testing.T) {
	if (Config{Dir: "x", Interval: 0, Keep: 7}).Enabled() {
		t.Error("an interval of zero must turn copies off")
	}
	if !(Config{Dir: "x", Interval: time.Hour, Keep: 1}).Enabled() {
		t.Error("a complete configuration must be enabled")
	}
}
