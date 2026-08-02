package migrations

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

func TestMigrationsApplyFromEmptySQLite(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	goose.SetBaseFS(FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, "."); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected at least one applied migration")
	}
}

// Migration 024 adds servers.folder_id, which SQLite refuses to drop while an
// index still references it — so the Down leg has to drop servers_folder_id
// first. Rolling down and back up must also leave existing servers intact.
func TestServerFoldersMigrationRollsBack(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	goose.SetBaseFS(FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, "."); err != nil {
		t.Fatal(err)
	}

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO nodes (id, name, fqdn, port, scheme, token) VALUES ('n1','n','localhost',8090,'http','x')`)
	mustExec(`INSERT INTO users (id, email, password_hash, role) VALUES ('u1','u@e.com','x','admin')`)
	mustExec(`INSERT INTO server_folders (id, name) VALUES ('f1','Minigames')`)
	mustExec(`INSERT INTO servers (id, node_id, owner_id, name, platform, mc_version, directory_path, java_binary, port, folder_id)
	          VALUES ('s1','n1','u1','bedwars','paper','1.21','servers/bedwars','java',25565,'f1')`)

	if err := goose.Down(db, "."); err != nil {
		t.Fatalf("rolling back the folders migration failed: %v", err)
	}

	// The server outlives the rollback; only the grouping is gone.
	var name string
	if err := db.QueryRow(`SELECT name FROM servers WHERE id='s1'`).Scan(&name); err != nil {
		t.Fatalf("server should survive the rollback: %v", err)
	}
	if name != "bedwars" {
		t.Fatalf("server name = %q, want bedwars", name)
	}
	if _, err := db.Exec(`SELECT folder_id FROM servers`); err == nil {
		t.Fatal("folder_id column should be gone after rollback")
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("re-applying the folders migration failed: %v", err)
	}
	var folderID *string
	if err := db.QueryRow(`SELECT folder_id FROM servers WHERE id='s1'`).Scan(&folderID); err != nil {
		t.Fatal(err)
	}
	if folderID != nil {
		t.Fatalf("folder_id = %v, want NULL — the grouping is not restored by re-applying", *folderID)
	}
}

// Migration 023 backfills whitelist-rejection alerts for users who predate the
// feature — without which the alert exists but fires for nobody — while leaving
// anyone who already has a rule for it alone, including one they disabled.
func TestJoinDeniedSubscriptionBackfill(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	goose.SetBaseFS(FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, ".", 22); err != nil {
		t.Fatal(err)
	}

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO users (id, email, password_hash, role) VALUES ('u1','a@e.com','x','admin')`)
	mustExec(`INSERT INTO users (id, email, password_hash, role) VALUES ('u2','b@e.com','x','user')`)
	// u2 already decided they do not want these alerts.
	mustExec(`INSERT INTO notification_subscriptions (id, user_id, event_type, server_id, channels, enabled)
	          VALUES ('sub-existing','u2','player.join_denied',NULL,'[]',0)`)

	if err := goose.Up(db, "."); err != nil {
		t.Fatal(err)
	}

	var channels string
	var enabled int
	if err := db.QueryRow(`SELECT channels, enabled FROM notification_subscriptions
	                        WHERE user_id='u1' AND event_type='player.join_denied'`).
		Scan(&channels, &enabled); err != nil {
		t.Fatalf("u1 should have been backfilled: %v", err)
	}
	if channels != `["inapp"]` || enabled != 1 {
		t.Errorf("backfilled rule = %s/%d, want [\"inapp\"] enabled", channels, enabled)
	}

	var id string
	if err := db.QueryRow(`SELECT id FROM notification_subscriptions
	                        WHERE user_id='u2' AND event_type='player.join_denied'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if id != "sub-existing" {
		t.Errorf("u2's own rule was replaced (id %s); an opt-out must survive the upgrade", id)
	}
}

// Migration 018 must remove duplicate managed-mod rows (keeping the oldest per
// project+dir) and then reject new duplicates via the unique index.
func TestModProjectUniqueMigrationDedupes(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	goose.SetBaseFS(FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, ".", 17); err != nil {
		t.Fatal(err)
	}

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO nodes (id, name, fqdn, port, scheme, token) VALUES ('n1','n','localhost',8090,'http','x')`)
	mustExec(`INSERT INTO users (id, email, password_hash, role) VALUES ('u1','u@e.com','x','admin')`)
	mustExec(`INSERT INTO servers (id, node_id, owner_id, name, platform, mc_version, directory_path, java_binary, port)
	          VALUES ('s1','n1','u1','smp','fabric','1.21','servers/smp','java',25565)`)

	insMod := `INSERT INTO installed_mods (id, server_id, source, source_id, name, version, file_name, install_path, installed_at)
	           VALUES (?,?,?,?,?,?,?,?,?)`
	// Duplicate pair: keep the older row ("orig"), drop the newer one ("dup").
	mustExec(insMod, "orig", "s1", "modrinth", "P7dR8mSH", "Fabric API", "1.0", "fabric-api.jar", "/mods", "2026-06-01 00:00:00")
	mustExec(insMod, "dup", "s1", "modrinth", "P7dR8mSH", "Fabric API", "1.0", "fabric-api.jar", "/mods", "2026-07-01 00:00:00")
	// Same project in a different dir is not a duplicate.
	mustExec(insMod, "otherdir", "s1", "modrinth", "P7dR8mSH", "Fabric API", "1.0", "fabric-api.jar", "/plugins", "2026-07-01 00:00:00")
	// Custom rows (NULL source_id) are never deduped.
	mustExec(insMod, "c1", "s1", "custom", nil, "a", "custom", "a.jar", "/mods", "2026-06-01 00:00:00")
	mustExec(insMod, "c2", "s1", "custom", nil, "b", "custom", "b.jar", "/mods", "2026-06-01 00:00:00")

	if err := goose.Up(db, "."); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Query(`SELECT id FROM installed_mods ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	want := []string{"c1", "c2", "orig", "otherdir"}
	if len(ids) != len(want) {
		t.Fatalf("surviving rows = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("surviving rows = %v, want %v", ids, want)
		}
	}

	// New duplicates are rejected outright.
	if _, err := db.Exec(insMod, "dup2", "s1", "modrinth", "P7dR8mSH", "Fabric API", "1.0", "fabric-api-2.jar", "/mods", "2026-07-02 00:00:00"); err == nil {
		t.Fatal("expected unique index to reject duplicate project row")
	}
	// A second NULL-source_id row is still fine.
	if _, err := db.Exec(insMod, "c3", "s1", "custom", nil, "c", "custom", "c.jar", "/mods", "2026-07-02 00:00:00"); err != nil {
		t.Fatalf("custom row insert should not be constrained: %v", err)
	}
}
