package handlers

import (
	"strings"
	"testing"

	"github.com/mcsm/api/internal/store"
)

// intersectWithScopes is what /servers and /members/me report to an agent, so
// it has to name exactly the permissions the route gate would allow: never
// more (that sends automation at routes it will be refused on) and never less
// (that hides work it is entitled to do).
func TestIntersectWithScopes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		perms  []string
		scopes []string
		want   []string
	}{
		{
			name:  "a leaf scope survives its owner's group grant",
			perms: []string{"view", "power"}, scopes: []string{"view", "power.restart"},
			want: []string{"view", "power.restart"},
		},
		{
			name:  "a group scope narrows to the owner's leaf",
			perms: []string{"view", "power.restart"}, scopes: []string{"view", "power"},
			want: []string{"view", "power.restart"},
		},
		{
			name:  "a group scope keeps every leaf the owner holds, in grant order",
			perms: []string{"view", "files.read", "files.delete"}, scopes: []string{"view", "files"},
			want: []string{"view", "files.read", "files.delete"},
		},
		{
			name:  "a group scope with no overlapping leaf reports nothing for it",
			perms: []string{"view", "files.read"}, scopes: []string{"view", "power"},
			want: []string{"view"},
		},
		{
			name:  "a leaf scope the owner does not hold is dropped, not widened",
			perms: []string{"view", "power.restart"}, scopes: []string{"view", "power.kill"},
			want: []string{"view"},
		},
		{
			name:  "a sibling leaf is never inferred from a group scope",
			perms: []string{"view", "power.restart"}, scopes: []string{"power", "power.kill"},
			want: []string{"power.restart"},
		},
		{
			name:  "an overlapping group and leaf scope report the leaf once",
			perms: []string{"view", "power.restart"}, scopes: []string{"power", "power.restart"},
			want: []string{"power.restart"},
		},
		{
			name:  "an owner who holds the group keeps the group scope itself",
			perms: []string{"view", "power"}, scopes: []string{"power"},
			want: []string{"power"},
		},
		{
			name:  "an admin owner satisfies every scope the key carries",
			perms: store.AllServerPermissions(), scopes: []string{"view", "power.restart", "files.read"},
			want: []string{"view", "power.restart", "files.read"},
		},
		{
			name:  "an owner with nothing here reports nothing",
			perms: nil, scopes: []string{"view", "power"},
			want: []string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := intersectWithScopes(tc.perms, tc.scopes)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got %v, want %v (order and duplicates matter)", got, tc.want)
			}
			// Whatever comes out must be backed by both sides, or the report
			// would claim authority the gate refuses.
			for _, perm := range got {
				if !store.HasServerPermission(tc.perms, store.ServerPermission(perm)) {
					t.Fatalf("%q is not backed by the owner's grants %v", perm, tc.perms)
				}
				if !store.HasServerPermission(tc.scopes, store.ServerPermission(perm)) {
					t.Fatalf("%q is not backed by the key's scopes %v", perm, tc.scopes)
				}
			}
		})
	}
}
