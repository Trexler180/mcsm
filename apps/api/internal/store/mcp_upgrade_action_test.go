package store

import "testing"

func TestMCPUpgradeActionValidationAndPermission(t *testing.T) {
	for _, target := range []string{"1.21.5", "26.3", "25w14a", "1.21.5-rc1"} {
		action, ok := MCPUpgradeAction(target)
		if !ok {
			t.Fatalf("valid target %q rejected", target)
		}
		if got, ok := ParseMCPUpgradeAction(action); !ok || got != target {
			t.Fatalf("ParseMCPUpgradeAction(%q) = %q, %v", action, got, ok)
		}
		if permission, ok := MCPActionPermission(action); !ok || permission != ServerPermissionSettings {
			t.Fatalf("upgrade permission = %q, %v", permission, ok)
		}
	}
	for _, target := range []string{"", "../26.3", "26.3?x=1", "26.3:restore"} {
		if _, ok := MCPUpgradeAction(target); ok {
			t.Fatalf("invalid target %q accepted", target)
		}
	}
}
