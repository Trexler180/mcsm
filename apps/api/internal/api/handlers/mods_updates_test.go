package handlers

import "testing"

func TestUpdatedModFilenamePreservesDisabledState(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		enabled  bool
		want     string
	}{
		{name: "enabled", filename: "mod.jar", enabled: true, want: "mod.jar"},
		{name: "disabled", filename: "mod.jar", enabled: false, want: "mod.jar.disabled"},
		{name: "already suffixed", filename: "mod.jar.disabled", enabled: false, want: "mod.jar.disabled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := updatedModFilename(tt.filename, tt.enabled); got != tt.want {
				t.Fatalf("updatedModFilename(%q, %v) = %q, want %q", tt.filename, tt.enabled, got, tt.want)
			}
		})
	}
}
