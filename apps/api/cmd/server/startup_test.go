package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlaceholderSecrets(t *testing.T) {
	for _, v := range []string{"CHANGE_ME", "changeme", " dev-secret ", "dev-agent-token", "local-dev-jwt-secret-change-me"} {
		if !isPlaceholderSecret(v) {
			t.Errorf("%q should be refused as a placeholder", v)
		}
	}
	for _, v := range []string{"", "2f6c1d0e9b7a4c53", "correct horse battery staple"} {
		if isPlaceholderSecret(v) {
			t.Errorf("%q is not a placeholder", v)
		}
	}
}

func keyEnv(t *testing.T, file string) {
	t.Helper()
	t.Setenv("APP_ENCRYPTION_KEY", "")
	t.Setenv("APP_ENCRYPTION_KEY_FILE", file)
}

// The first boot creates the key; every later boot reads the same one back.
func TestMasterKeyIsGeneratedOnceAndKept(t *testing.T) {
	dir := t.TempDir()
	keyEnv(t, "")
	db := filepath.Join(dir, "mcsm.db")

	first, err := resolveMasterKey(db, "jwt", false)
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, ".mcsm-secret-key"))
	if err != nil || strings.TrimSpace(string(onDisk)) != first || first == "jwt" {
		t.Fatalf("key file = %q, %v; returned %q", onDisk, err, first)
	}
	second, err := resolveMasterKey(db, "jwt", false)
	if err != nil || second != first {
		t.Fatalf("second boot key = %q, %v; want %q", second, err, first)
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".mcsm-secret-key-*"))
	if len(left) != 0 {
		t.Fatalf("temporary key files left behind: %v", left)
	}
}

func TestMasterKeyFromEnvironmentWins(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APP_ENCRYPTION_KEY", "from-env")
	t.Setenv("APP_ENCRYPTION_KEY_FILE", filepath.Join(dir, "key"))
	got, err := resolveMasterKey(filepath.Join(dir, "mcsm.db"), "jwt", false)
	if err != nil || got != "from-env" {
		t.Fatalf("key = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "key")); !os.IsNotExist(err) {
		t.Fatalf("a key file was written although the environment holds the key: %v", err)
	}
}

// A key file that cannot be created must stop a production panel rather than
// have it save secrets under a stand-in key; development carries on.
func TestMasterKeyFailsClosedWhenTheFileCannotBeCreated(t *testing.T) {
	missingDir := filepath.Join(t.TempDir(), "not-there", "key")
	keyEnv(t, missingDir)

	if k, err := resolveMasterKey("mcsm.db", "jwt", false); err == nil {
		t.Fatalf("production started with key %q and no key file", k)
	} else if !strings.Contains(err.Error(), "APP_ENCRYPTION_KEY") {
		t.Fatalf("the error does not say how to fix it: %v", err)
	}
	if k, err := resolveMasterKey("mcsm.db", "jwt", true); err != nil || k != "jwt" {
		t.Fatalf("development fallback = %q, %v; want the JWT secret", k, err)
	}
}

// An empty key file is a damaged one. It must never be silently replaced:
// secrets may already be encrypted under the key it used to hold.
func TestMasterKeyRefusesAnEmptyKeyFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(file, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	keyEnv(t, file)
	if k, err := resolveMasterKey("mcsm.db", "jwt", false); err == nil {
		t.Fatalf("started with key %q over an empty key file", k)
	}
	if b, _ := os.ReadFile(file); strings.TrimSpace(string(b)) != "" {
		t.Fatalf("the empty key file was overwritten with %q", b)
	}
}

func TestDBBackupConfig(t *testing.T) {
	db := filepath.Join("data", "mcsm.db")
	for _, k := range []string{"DB_BACKUP_DIR", "DB_BACKUP_INTERVAL_HOURS", "DB_BACKUP_KEEP"} {
		t.Setenv(k, "")
	}
	cfg := dbBackupConfig(db)
	if cfg.Dir != filepath.Join("data", "db-backups") || cfg.Interval != 24*time.Hour || cfg.Keep != 7 || !cfg.Enabled() {
		t.Fatalf("defaults = %+v", cfg)
	}

	t.Setenv("DB_BACKUP_INTERVAL_HOURS", "0")
	if dbBackupConfig(db).Enabled() {
		t.Fatal("DB_BACKUP_INTERVAL_HOURS=0 must turn copies off")
	}

	// A typo keeps the default instead of silently turning copies off.
	t.Setenv("DB_BACKUP_INTERVAL_HOURS", "daily")
	t.Setenv("DB_BACKUP_KEEP", "0")
	t.Setenv("DB_BACKUP_DIR", "/mnt/offsite")
	cfg = dbBackupConfig(db)
	if cfg.Interval != 24*time.Hour || cfg.Keep != 1 || cfg.Dir != "/mnt/offsite" {
		t.Fatalf("config = %+v, want a 24h interval, one copy kept, the given dir", cfg)
	}
}
