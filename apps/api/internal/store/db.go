package store

import (
	"database/sql"
	"time"

	"github.com/robfig/cron/v3"
)

// NextRunForCron returns the next time a 5-field cron expression fires after
// `from`, or nil when the expression can't be parsed. Uses the same standard
// parser the scheduler runs on, so the stored next_run matches reality.
func NextRunForCron(expr string, from time.Time) *time.Time {
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return nil
	}
	n := sched.Next(from)
	return &n
}

type Store struct {
	db        *sql.DB
	secretKey []byte // AES-256 key for app_secrets; nil disables secret storage
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// WithEncryption sets the master secret used to encrypt app_secrets at rest and
// returns the same store for chaining. An empty master leaves secret storage
// disabled (the secret methods then return a clear error).
func (s *Store) WithEncryption(master string) *Store {
	s.secretKey = deriveKey(master)
	return s
}
