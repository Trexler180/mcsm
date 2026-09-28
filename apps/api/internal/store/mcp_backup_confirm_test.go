package store

import (
	"context"
	"errors"
	"testing"
)

// One human answer authorizes one backup. The tool's description promises a
// separate confirmation every time, and this is what makes that enforceable
// rather than aspirational: the state used to be a fixed hash of grant and
// server, so a client that kept the first one could start a backup whenever it
// liked, forever, on a single "yes".
func TestBackupConfirmationIsSingleUse(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID)

	state, err := s.CreateMCPBackupConfirmation(ctx, grant.ID, serverID)
	if err != nil {
		t.Fatal(err)
	}
	if state == "" {
		t.Fatal("no confirmation state was issued")
	}

	if err := s.ConsumeMCPBackupConfirmation(ctx, state, grant.ID, serverID); err != nil {
		t.Fatalf("first use was refused: %v", err)
	}
	if err := s.ConsumeMCPBackupConfirmation(ctx, state, grant.ID, serverID); !errors.Is(err, ErrMCPConfirmationInvalid) {
		t.Fatalf("a spent confirmation was accepted again: %v", err)
	}
}

// A confirmation answers the question it was asked. Presenting it for another
// grant or another server proves nothing about either.
func TestBackupConfirmationIsBoundToItsGrantAndServer(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID)

	state, err := s.CreateMCPBackupConfirmation(ctx, grant.ID, serverID)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.ConsumeMCPBackupConfirmation(ctx, state, grant.ID, "some-other-server"); !errors.Is(err, ErrMCPConfirmationInvalid) {
		t.Fatalf("a confirmation was accepted for another server: %v", err)
	}
	if err := s.ConsumeMCPBackupConfirmation(ctx, state, "some-other-grant", serverID); !errors.Is(err, ErrMCPConfirmationInvalid) {
		t.Fatalf("a confirmation was accepted for another grant: %v", err)
	}
	// A mismatched presentation must not burn the state either, or anyone who
	// could guess a grant id could stop a legitimate backup from ever starting.
	if err := s.ConsumeMCPBackupConfirmation(ctx, state, grant.ID, serverID); err != nil {
		t.Fatalf("a rejected presentation consumed the confirmation: %v", err)
	}
}

// Unknown and malformed states are refused with the same error as everything
// else, so a client cannot learn which of its guesses was closer.
func TestBackupConfirmationRejectsUnknownState(t *testing.T) {
	ctx := context.Background()
	s, ownerID, serverID, client := mcpFixture(t)
	grant, _ := mustGrant(t, s, client, ownerID, serverID)

	for _, state := range []string{"", "not-a-state", mcpConfirmPrefix + "invented"} {
		if err := s.ConsumeMCPBackupConfirmation(ctx, state, grant.ID, serverID); !errors.Is(err, ErrMCPConfirmationInvalid) {
			t.Errorf("state %q was accepted: %v", state, err)
		}
	}
}
