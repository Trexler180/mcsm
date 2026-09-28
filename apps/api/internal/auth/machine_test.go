package auth

import (
	"context"
	"testing"
)

// Route guards ask this one question instead of checking one credential family
// and forgetting the other — which is exactly the bug it was added to close.
func TestIsMachineCoversBothCredentialFamilies(t *testing.T) {
	if IsMachine(context.Background()) {
		t.Error("a plain context reads as a machine")
	}

	keyed := context.WithValue(context.Background(), machineKey, &MachinePrincipal{KeyID: "key-1"})
	if !IsMachine(keyed) {
		t.Error("an access key does not read as a machine")
	}

	delegated := WithDelegatedActor(context.Background(), &DelegatedActor{
		UserID: "user-1", GrantID: "grant-1",
	})
	if !IsMachine(delegated) {
		t.Error("an MCP grant does not read as a machine")
	}
}
