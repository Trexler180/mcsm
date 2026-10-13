package store

import "testing"

// Every permission a preset or the model names is one the API accepts, and
// the model covers every grantable group.
func TestPermissionPresetsUseRealPermissions(t *testing.T) {
	valid := map[string]bool{}
	for _, g := range PermissionModel() {
		valid[g.Group] = true
		for _, l := range g.Leaves {
			valid[l.Permission] = true
		}
	}
	for _, p := range AllServerPermissions() {
		if !valid[p] {
			t.Errorf("group %s is missing from the model", p)
		}
	}
	for _, preset := range PermissionPresets() {
		if _, err := NormalizeServerPermissions(preset.Permissions); err != nil {
			t.Errorf("preset %s: %v", preset.ID, err)
		}
		for _, p := range preset.Permissions {
			if !valid[p] {
				t.Errorf("preset %s names unknown permission %s", preset.ID, p)
			}
		}
	}
	if perms, ok := PermissionPresetByID("viewer"); !ok || len(perms) != 1 || perms[0] != "view" {
		t.Fatalf("viewer preset = %v, %v", perms, ok)
	}
}
