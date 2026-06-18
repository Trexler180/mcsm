package store

import (
	"testing"
	"time"
)

func TestNextRunForCron(t *testing.T) {
	// Fixed reference: 2026-06-17 12:00:00 local.
	from := time.Date(2026, 6, 17, 12, 0, 0, 0, time.Local)

	// Daily at 03:00 → next is tomorrow 03:00.
	if got := NextRunForCron("0 3 * * *", from); got == nil {
		t.Fatal("daily: got nil")
	} else if !got.After(from) {
		t.Errorf("daily: next %v not after %v", got, from)
	} else if got.Hour() != 3 || got.Day() != 18 {
		t.Errorf("daily: got %v, want 2026-06-18 03:00", got)
	}

	// Every 3 minutes → next is 12:03, strictly in the future.
	if got := NextRunForCron("*/3 * * * *", from); got == nil {
		t.Fatal("every-3-min: got nil")
	} else if !got.After(from) {
		t.Errorf("every-3-min: next %v not after %v", got, from)
	} else if d := got.Sub(from); d <= 0 || d > 3*time.Minute {
		t.Errorf("every-3-min: delta %v, want (0, 3m]", d)
	}

	// Invalid expression → nil (caller leaves next_run untouched).
	if got := NextRunForCron("not a cron", from); got != nil {
		t.Errorf("invalid: got %v, want nil", got)
	}
}
