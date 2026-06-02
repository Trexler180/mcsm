package handlers

import (
	"net/http"

	"github.com/mcsm/agent/internal/java"
)

func JavaInstallations(w http.ResponseWriter, r *http.Request) {
	installs := java.Detect()
	writeJSON(w, http.StatusOK, map[string]any{"installations": installs})
}
