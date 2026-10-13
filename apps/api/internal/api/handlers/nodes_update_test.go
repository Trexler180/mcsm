package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The API sends a node's token with every call to it, so moving a node to a
// new address without re-entering the token would deliver the token to that
// address. The move must come with the token.
func TestNodeAddressChangeNeedsToken(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	admin := f.user("admin@example.com", "admin")
	h := NewNodeHandlers(f.store, nil)
	put := func(body string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		h.Update(rr, withURLParams(as(admin, "admin", http.MethodPut, "/", strings.NewReader(body)), "id", f.node.ID))
		return rr
	}

	if rr := put(`{"fqdn":"attacker.example"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("address change without token: status %d, want 400", rr.Code)
	}
	n, _ := f.store.GetNode(ctx, f.node.ID)
	if n.FQDN != f.node.FQDN {
		t.Fatal("the refused change was applied anyway")
	}

	if rr := put(`{"name":"renamed"}`); rr.Code != http.StatusOK {
		t.Fatalf("a rename needs no token: status %d", rr.Code)
	}
	if rr := put(`{"fqdn":"new-host.example","token":"fresh-token"}`); rr.Code != http.StatusOK {
		t.Fatalf("address change with token: status %d %s", rr.Code, rr.Body.String())
	}
	n, _ = f.store.GetNode(ctx, f.node.ID)
	if n.FQDN != "new-host.example" || n.Token != "fresh-token" {
		t.Fatalf("node = %s / token %q, want the new address and token", n.FQDN, n.Token)
	}
	if rr := put(`{"scheme":"ftp","token":"x"}`); rr.Code != http.StatusBadRequest {
		t.Fatalf("an unknown scheme must be refused, got %d", rr.Code)
	}
}
