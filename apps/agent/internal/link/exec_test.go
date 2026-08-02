package link

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The tests below are all really about one question: after this error, is it
// safe to run the same command again over stdin? Getting that wrong in the
// permissive direction executes an operator's command twice, so each failure
// mode is pinned individually rather than covered by one happy-path test.

// linkedSession dials in as a mod would and returns the live connection.
func linkedSession(t *testing.T, h *harness, ctx context.Context, serverID, token string) *websocket.Conn {
	t.Helper()

	conn, _, err := h.dial(t, ctx, serverID, token)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	sendHello(t, ctx, conn)

	select {
	case <-h.sink.connectCh:
	case <-time.After(5 * time.Second):
		t.Fatal("sink never saw the connection")
	}
	return conn
}

// replyOnce plays the mod: reads one RPC request and answers it.
func replyOnce(t *testing.T, ctx context.Context, conn *websocket.Conn, resp RPCResponse) {
	t.Helper()
	go func() {
		env := readFrame(t, ctx, conn)
		if env.Type != TypeRPCRequest {
			return
		}
		writeFrame(t, ctx, conn, Envelope{
			V:    ProtocolVersion,
			Type: TypeRPCResponse,
			ID:   env.ID,
			TS:   time.Now().UnixMilli(),
			Data: mustMarshal(resp),
		})
	}()
}

func TestExecCommandWithNoModIsRetryable(t *testing.T) {
	h := newHarness(t, "srv1", "secret-token")

	_, err := h.registry.ExecCommand(testContext(t), "srv1", "list")
	if !errors.Is(err, ErrNoSession) {
		t.Fatalf("want ErrNoSession, got %v", err)
	}
}

func TestExecCommandReturnsCapturedOutput(t *testing.T) {
	h := newHarness(t, "srv1", "secret-token")
	ctx := testContext(t)
	conn := linkedSession(t, h, ctx, "srv1", "secret-token")

	replyOnce(t, ctx, conn, RPCResponse{
		OK:     true,
		Result: mustMarshal(ExecResult{Output: []string{"There are 1 of a max of 20 players online"}, Success: true}),
	})

	result, err := h.registry.ExecCommand(ctx, "srv1", "list")
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !result.Success {
		t.Error("want success")
	}
	if len(result.Output) != 1 {
		t.Fatalf("want 1 output line, got %v", result.Output)
	}
}

// A command the server ran and rejected is a fact about the command, not about
// the link. It must not look like a delivery failure, or the caller would run it
// a second time over stdin.
func TestExecCommandFailedCommandIsNotAnError(t *testing.T) {
	h := newHarness(t, "srv1", "secret-token")
	ctx := testContext(t)
	conn := linkedSession(t, h, ctx, "srv1", "secret-token")

	replyOnce(t, ctx, conn, RPCResponse{
		OK:     true,
		Result: mustMarshal(ExecResult{Output: []string{"Unknown or incomplete command"}, Success: false}),
	})

	result, err := h.registry.ExecCommand(ctx, "srv1", "wat")
	if err != nil {
		t.Fatalf("want no error for a rejected command, got %v", err)
	}
	if result.Success {
		t.Error("want success=false")
	}
}

// A mod that refuses the method outright — an older build without command.exec —
// never executed anything, so the console must be able to fall back.
func TestExecCommandRefusedMethodIsRetryable(t *testing.T) {
	h := newHarness(t, "srv1", "secret-token")
	ctx := testContext(t)
	conn := linkedSession(t, h, ctx, "srv1", "secret-token")

	replyOnce(t, ctx, conn, RPCResponse{
		OK:    false,
		Error: &RPCError{Code: "unknown_method", Message: "no such method: command.exec"},
	})

	if _, err := h.registry.ExecCommand(ctx, "srv1", "list"); !errors.Is(err, ErrNotDelivered) {
		t.Fatalf("want ErrNotDelivered, got %v", err)
	}
}

// The important negative case. A timed-out call may well have executed, so it
// must NOT wrap ErrNotDelivered — otherwise the caller reruns it over stdin and
// the command happens twice.
func TestExecCommandTimeoutIsNotRetryable(t *testing.T) {
	h := newHarness(t, "srv1", "secret-token")
	ctx := testContext(t)
	linkedSession(t, h, ctx, "srv1", "secret-token")

	// No reply is ever sent. Bound the wait with the caller's context rather than
	// the 15s internal ceiling.
	callCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()

	_, err := h.registry.ExecCommand(callCtx, "srv1", "save-all")
	if err == nil {
		t.Fatal("want an error")
	}
	if errors.Is(err, ErrNotDelivered) || errors.Is(err, ErrNoSession) {
		t.Fatalf("a timed-out command must not be marked retryable: %v", err)
	}
}

// A session that closed before the request was written provably delivered
// nothing, so this one is retryable.
func TestExecCommandOnClosedSessionIsRetryable(t *testing.T) {
	h := newHarness(t, "srv1", "secret-token")
	ctx := testContext(t)
	conn := linkedSession(t, h, ctx, "srv1", "secret-token")

	session, ok := h.registry.Get("srv1")
	if !ok {
		t.Fatal("session not registered")
	}
	conn.Close(websocket.StatusNormalClosure, "")

	// Wait for the agent side to notice the close.
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session never finished")
	}

	_, err := session.Call(ctx, MethodCommandExec, map[string]string{"command": "list"})
	if !errors.Is(err, ErrNotDelivered) {
		t.Fatalf("want ErrNotDelivered, got %v", err)
	}
}
