package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mcsm/api/internal/dbbackup"
)

// placeholderSecrets are the values the docs, the Makefile and the dev
// launchers use where a real secret belongs. Outside development mode none of
// them may guard anything: each is published in this repository.
var placeholderSecrets = map[string]bool{
	"change_me":                      true,
	"change-me":                      true,
	"changeme":                       true,
	"dev-secret":                     true,
	"dev-agent-token":                true,
	"local-dev-jwt-secret-change-me": true,
}

func isPlaceholderSecret(v string) bool {
	return placeholderSecrets[strings.ToLower(strings.TrimSpace(v))]
}

// resolveMasterKey returns the master secret used to encrypt app secrets at
// rest, chosen so stored keys survive restarts without re-entry. Precedence:
//
//  1. APP_ENCRYPTION_KEY — explicit, strongest: the key lives only in the
//     environment, never on disk, and is unaffected by anything else.
//  2. A persistent key auto-generated once and stored beside the database
//     (override the path with APP_ENCRYPTION_KEY_FILE). This is the zero-config
//     default: it survives restarts and JWT_SECRET rotation, so a key pasted in
//     Settings stays valid. At-rest protection here is only as strong as the
//     file permissions on the key file.
//
// When the key file can be neither read nor created, production refuses to
// start. Carrying on with a stand-in key would encrypt whatever is saved next
// under a key that stops being used the moment the file is fixed, and those
// secrets (node tokens, two-factor secrets) would be unreadable from then on.
// Development mode falls back to the JWT secret instead, so a throwaway
// checkout on a read-only directory still runs.
//
// A key file that exists is never replaced: only a missing one is generated.
func resolveMasterKey(dbPath, jwtSecret string, dev bool) (string, error) {
	if k := os.Getenv("APP_ENCRYPTION_KEY"); k != "" {
		return k, nil
	}

	keyPath := os.Getenv("APP_ENCRYPTION_KEY_FILE")
	if keyPath == "" {
		keyPath = filepath.Join(filepath.Dir(dbPath), ".mcsm-secret-key")
	}
	fallback := func(problem error) (string, error) {
		if !dev {
			return "", fmt.Errorf("%w; make it readable and writable by the API's user, or set APP_ENCRYPTION_KEY. "+
				"If this panel has been running without a key file, its secrets were encrypted with JWT_SECRET: "+
				"set APP_ENCRYPTION_KEY to that value to keep reading them", problem)
		}
		log.Printf("%v; falling back to the JWT secret, so stored secrets will not survive a restart unless APP_ENCRYPTION_KEY is set", problem)
		return jwtSecret, nil
	}

	b, err := os.ReadFile(keyPath)
	switch {
	case err == nil:
		if k := strings.TrimSpace(string(b)); k != "" {
			return k, nil
		}
		return fallback(fmt.Errorf("the key file %s is empty (restore it from a backup, or delete it to start with a new key)", keyPath))
	case !errors.Is(err, fs.ErrNotExist):
		return fallback(fmt.Errorf("could not read the key file %s: %v", keyPath, err))
	}

	k, err := randomHex(32)
	if err != nil {
		return fallback(fmt.Errorf("could not generate a key: %v", err))
	}
	if err := writeKeyFile(keyPath, k); err != nil {
		return fallback(fmt.Errorf("could not create the key file %s: %v", keyPath, err))
	}
	log.Printf("generated persistent app encryption key at %s", keyPath)
	return k, nil
}

// writeKeyFile creates the key file whole or not at all: written beside its
// final name, synced, then renamed into place, so a crash cannot leave an empty
// or partial key for the next boot to guess about. (A rename rather than a hard
// link: not every filesystem a panel is run from has links.)
func writeKeyFile(path, key string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcsm-secret-key-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	// Best effort where modes exist; CreateTemp already made it 0600 there.
	_ = tmp.Chmod(0o600)
	if _, err := tmp.WriteString(key); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// dbBackupConfig reads how the panel copies its own database:
//
//	DB_BACKUP_DIR             where copies go (default: db-backups beside the database)
//	DB_BACKUP_INTERVAL_HOURS  how often (default 24; 0 turns copies off)
//	DB_BACKUP_KEEP            how many to keep (default 7)
//
// A value that is not a number falls back to its default rather than turning
// the copies off: a typo should not cost someone their backups.
func dbBackupConfig(dbPath string) dbbackup.Config {
	dir := os.Getenv("DB_BACKUP_DIR")
	if dir == "" {
		dir = filepath.Join(filepath.Dir(dbPath), "db-backups")
	}
	return dbbackup.Config{
		Dir:      dir,
		Interval: time.Duration(envInt("DB_BACKUP_INTERVAL_HOURS", 24)) * time.Hour,
		Keep:     max(1, envInt("DB_BACKUP_KEEP", 7)),
	}
}

// envInt reads a non-negative whole number, or def when the variable is unset
// or is not one.
func envInt(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		log.Printf("%s=%q is not a whole number; using %d", key, raw, def)
		return def
	}
	return n
}
