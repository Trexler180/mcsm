package process

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// ExecCommand's whole job is deciding when the stdin fallback is allowed. These
// tests pin that decision, because both ways of getting it wrong are bad: too
// permissive runs an operator's command twice, too strict makes an unlinked
// server's console stop working.

func TestExecCommandPrefersTheModWhenLinked(t *testing.T) {
	m := NewManager(t.TempDir())

	var got string
	m.SetLinkExec(func(_ context.Context, serverID, cmd string) (ExecOutcome, error) {
		got = cmd
		return ExecOutcome{ViaMod: true, Success: true, Output: []string{"ok"}}, nil
	})

	outcome, err := m.ExecCommand(context.Background(), "srv1", "list")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if got != "list" {
		t.Errorf("mod saw %q, want \"list\"", got)
	}
	if !outcome.ViaMod || !outcome.Success {
		t.Errorf("unexpected outcome: %+v", outcome)
	}
}

// The server is not running, so the stdin fallback fails too — but the point is
// that it was *attempted*. A missing mod must not take the console down with it.
func TestExecCommandFallsBackWhenLinkUnavailable(t *testing.T) {
	m := NewManager(t.TempDir())

	m.SetLinkExec(func(context.Context, string, string) (ExecOutcome, error) {
		return ExecOutcome{}, fmt.Errorf("%w: no session", ErrLinkUnavailable)
	})

	outcome, err := m.ExecCommand(context.Background(), "srv1", "list")
	if err == nil || err.Error() != "server not running" {
		t.Fatalf("want the stdin path's error, got %v", err)
	}
	if outcome.ViaMod {
		t.Error("outcome claims the mod handled it")
	}
}

// The critical one: an error that is not ErrLinkUnavailable means the command
// may already have run, so ExecCommand must surface it rather than reissue the
// command over stdin.
func TestExecCommandDoesNotRetryAmbiguousFailures(t *testing.T) {
	m := NewManager(t.TempDir())

	timeout := errors.New("link: command.exec timed out")
	m.SetLinkExec(func(context.Context, string, string) (ExecOutcome, error) {
		return ExecOutcome{}, timeout
	})

	_, err := m.ExecCommand(context.Background(), "srv1", "op someone")
	if !errors.Is(err, timeout) {
		t.Fatalf("want the link error surfaced, got %v", err)
	}
	// "server not running" would mean it fell through to stdin — a second
	// execution of a command that may already have taken effect.
	if err.Error() == "server not running" {
		t.Fatal("ambiguous link failure was retried over stdin")
	}
}

func TestExecCommandWithoutAModUsesStdin(t *testing.T) {
	m := NewManager(t.TempDir())

	outcome, err := m.ExecCommand(context.Background(), "srv1", "list")
	if err == nil || err.Error() != "server not running" {
		t.Fatalf("want the stdin path's error, got %v", err)
	}
	if outcome.ViaMod {
		t.Error("outcome claims the mod handled it")
	}
}

// Player actions issue console commands through SendCommand. Routing those over
// the link was explicitly out of scope, and the separation is structural — this
// pins it so a later refactor that folds ExecCommand into SendCommand cannot
// silently reroute moderation through the RPC dispatcher.
func TestPlayerActionsDoNotUseTheLink(t *testing.T) {
	m := NewManager(t.TempDir())

	called := false
	m.SetLinkExec(func(context.Context, string, string) (ExecOutcome, error) {
		called = true
		return ExecOutcome{ViaMod: true, Success: true}, nil
	})

	_ = m.SendCommand("srv1", "kick someone")

	if called {
		t.Error("SendCommand routed through the helper-mod link")
	}
}

func TestOnUnregisterFiresForPurgedServer(t *testing.T) {
	m := NewManager(t.TempDir())

	var forgotten []string
	m.OnUnregisterFunc(func(id string) { forgotten = append(forgotten, id) })

	m.Unregister("srv1")

	if len(forgotten) != 1 || forgotten[0] != "srv1" {
		t.Fatalf("unregister hook saw %v, want [srv1]", forgotten)
	}
}

func TestUnregisterWithoutHookDoesNotPanic(t *testing.T) {
	NewManager(t.TempDir()).Unregister("srv1")
}
