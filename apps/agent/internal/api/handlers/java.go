package handlers

import (
	"net/http"
	"runtime"

	"github.com/mcsm/agent/internal/java"
)

// JavaInstallations reports the Java runtimes found on this host plus the host
// OS, so the panel can offer to switch a server to a compatible version or, when
// none is installed, show OS-appropriate install instructions.
func JavaInstallations(w http.ResponseWriter, r *http.Request) {
	installs := java.Detect()
	writeJSON(w, http.StatusOK, map[string]any{
		"installations": installs,
		"os":            runtime.GOOS,
	})
}
