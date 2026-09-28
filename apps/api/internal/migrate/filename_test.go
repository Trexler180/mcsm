package migrate

import "testing"

func TestMigrationModFilenamePreservesDisabledState(t *testing.T) {
	for _, tt := range []struct {
		name, filename, want string
		enabled              bool
	}{
		{name: "enabled", filename: "mod.jar", enabled: true, want: "mod.jar"},
		{name: "disabled", filename: "mod.jar", enabled: false, want: "mod.jar.disabled"},
		{name: "already disabled", filename: "mod.jar.disabled", enabled: false, want: "mod.jar.disabled"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := migrationModFilename(tt.filename, tt.enabled); got != tt.want {
				t.Fatalf("migrationModFilename() = %q, want %q", got, tt.want)
			}
		})
	}
}
