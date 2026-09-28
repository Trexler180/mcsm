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

// Migration 026 turns the reserved api_keys table into a real capability. It
// has to be safe on an already-populated database: the rows that predate it
// must survive the upgrade and be unusable afterwards (empty capabilities, no
// expiry), and the rollback must not take the audit history with it.
func TestAgentAccessKeysMigrationIsAdditiveAndReversible(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	goose.SetBaseFS(FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	// Stop just before 026 and seed the schema as it stood then.
	if err := goose.UpTo(db, ".", 25); err != nil {
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
	          VALUES ('s1','n1','u1','smp','paper','1.21','servers/smp','java',25565)`)
	// A reserved row from before the feature existed.
	mustExec(`INSERT INTO api_keys (id, user_id, token_hash, name) VALUES ('k-old','u1','oldhash','reserved')`)
	mustExec(`INSERT INTO audit_log (user_id, server_id, action) VALUES ('u1','s1','server.start')`)

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("applying 026 over an existing database failed: %v", err)
	}

	// The legacy row survives, with capabilities that cannot authenticate.
	var scopes, servers string
	var expires *string
	if err := db.QueryRow(`SELECT scopes, server_ids, expires_at FROM api_keys WHERE id='k-old'`).
		Scan(&scopes, &servers, &expires); err != nil {
		t.Fatalf("the reserved row should survive the upgrade: %v", err)
	}
	if scopes != "[]" || servers != "[]" || expires != nil {
		t.Fatalf("legacy row = %s/%s/%v, want empty capabilities and no expiry", scopes, servers, expires)
	}

	// The hashed-token uniqueness constraint is preserved.
	if _, err := db.Exec(`INSERT INTO api_keys (id, user_id, token_hash, name) VALUES ('k-dupe','u1','oldhash','dupe')`); err == nil {
		t.Fatal("token_hash uniqueness was lost")
	}

	// Audit attribution is additive and nullable.
	mustExec(`INSERT INTO audit_log (user_id, api_key_id, server_id, action) VALUES ('u1','k-old','s1','server.restart')`)
	var keyed int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE api_key_id IS NOT NULL`).Scan(&keyed); err != nil {
		t.Fatal(err)
	}
	if keyed != 1 {
		t.Fatalf("api_key_id rows = %d, want 1", keyed)
	}

	// Rolling back drops only the added columns; the rows stay.
	if err := goose.DownTo(db, ".", 25); err != nil {
		t.Fatalf("rolling back the access-key migration failed: %v", err)
	}
	if _, err := db.Exec(`SELECT scopes FROM api_keys`); err == nil {
		t.Fatal("scopes column should be gone after rollback")
	}
	if _, err := db.Exec(`SELECT api_key_id FROM audit_log`); err == nil {
		t.Fatal("audit_log.api_key_id should be gone after rollback")
	}
	var keys, audits int
	if err := db.QueryRow(`SELECT COUNT(*) FROM api_keys`).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_log`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if keys != 1 || audits != 2 {
		t.Fatalf("rollback lost data: %d keys, %d audit rows", keys, audits)
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("re-applying the access-key migration failed: %v", err)
	}
}

// Migration 028 makes the MCP approval gate configurable. The security property
// worth a test is the one that is easy to get backwards: a database that
// upgrades and configures nothing must keep the 027 behavior exactly — every
// pre-existing grant still demands a step-up and auto-approves nothing.
func TestMCPApprovalSettingsMigrationDefaultsToSecure(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	goose.SetBaseFS(FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	// Stop just before 028 and seed a grant as the schema stood then.
	if err := goose.UpTo(db, ".", 27); err != nil {
		t.Fatal(err)
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(`INSERT INTO users (id, email, password_hash, role) VALUES ('u1','u@e.com','x','admin')`)
	mustExec(`INSERT INTO mcp_clients (client_id, client_name) VALUES ('c1','Claude Code')`)
	mustExec(`INSERT INTO mcp_grants (id, user_id, client_id, client_name, scopes, server_ids, resource, expires_at)
	          VALUES ('g-old','u1','c1','Claude Code','["mcp:actions.request"]','["s1"]','https://x/mcp','2099-01-01 00:00:00')`)

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("applying 028 over an existing database failed: %v", err)
	}

	// The pre-existing grant inherits rather than pinning a value.
	var reqPw, autoLife, autoUp *int64
	if err := db.QueryRow(`SELECT require_password, auto_approve_lifecycle, auto_approve_upgrades
	                         FROM mcp_grants WHERE id='g-old'`).Scan(&reqPw, &autoLife, &autoUp); err != nil {
		t.Fatalf("the existing grant should survive the upgrade: %v", err)
	}
	if reqPw != nil || autoLife != nil || autoUp != nil {
		t.Fatalf("legacy grant overrides = %v/%v/%v, want all NULL (inherit)", reqPw, autoLife, autoUp)
	}

	// And the user has no settings row at all, which must read as secure rather
	// than as an absence somebody fills in later.
	var settings int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_mcp_approval_settings WHERE user_id='u1'`).Scan(&settings); err != nil {
		t.Fatal(err)
	}
	if settings != 0 {
		t.Fatalf("settings rows = %d, want 0 — the secure default is the absence of a row", settings)
	}

	// The table's own defaults are the secure ones, so an INSERT that names no
	// columns cannot silently open the gate.
	mustExec(`INSERT INTO user_mcp_approval_settings (user_id) VALUES ('u1')`)
	var d1, d2, d3 int
	if err := db.QueryRow(`SELECT require_password, auto_approve_lifecycle, auto_approve_upgrades
	                         FROM user_mcp_approval_settings WHERE user_id='u1'`).Scan(&d1, &d2, &d3); err != nil {
		t.Fatal(err)
	}
	if d1 != 1 || d2 != 0 || d3 != 0 {
		t.Fatalf("column defaults = %d/%d/%d, want 1/0/0", d1, d2, d3)
	}

	// Rolling back drops only what 028 added; the grant stays.
	if err := goose.DownTo(db, ".", 27); err != nil {
		t.Fatalf("rolling back the approval-settings migration failed: %v", err)
	}
	if _, err := db.Exec(`SELECT require_password FROM mcp_grants`); err == nil {
		t.Fatal("require_password column should be gone after rollback")
	}
	if _, err := db.Exec(`SELECT 1 FROM user_mcp_approval_settings`); err == nil {
		t.Fatal("user_mcp_approval_settings should be gone after rollback")
	}
	var grants int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mcp_grants`).Scan(&grants); err != nil {
		t.Fatal(err)
	}
	if grants != 1 {
		t.Fatalf("rollback lost data: %d grants", grants)
	}

	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("re-applying the approval-settings migration failed: %v", err)
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

	// DownTo(23) rather than Down(): the latter only unwinds the newest
	// migration, so this would stop testing 024 the moment a 025 landed.
	if err := goose.DownTo(db, ".", 23); err != nil {
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
