package agent

import (
	"encoding/json"
	"strings"

	"github.com/mcsm/api/internal/store"
)

// StartConfigForServer builds the complete agent start payload for a server.
//
// This exists because the payload was previously assembled independently at
// every call site, and the ones that forgot a piece failed silently and
// destructively rather than loudly. The auto-update engine omitted the
// helper-mod flag, so its nightly restart told the agent the mod was disabled —
// and the agent dutifully deleted the jar and dropped the link env vars, which
// looked from the panel like telemetry mysteriously stopping at 04:00 while the
// toggle still read "on". The same omission dropped no_install, which on an
// imported server invites the agent to re-provision a runtime over somebody's
// existing files.
//
// So there is exactly one builder, it takes the whole server, and every start
// path uses it. A new caller cannot forget a field it never had to remember.
func StartConfigForServer(srv *store.Server) map[string]any {
	cfg := StartConfig(
		srv.DirectoryPath,
		srv.JavaBinary,
		srv.JVMArgs,
		srv.Platform,
		srv.MCVersion,
		srv.LoaderVersion,
		srv.RAMMbMin,
		srv.RAMMbMax,
	)
	applyImportConfig(cfg, srv.Settings)
	if HelperModEnabled(srv.Settings, srv.Platform, srv.MCVersion) {
		cfg["helper_mod"] = true
	}
	return cfg
}

// applyImportConfig threads a server's import metadata into the start payload:
// the detected jar to run, and the no-install flag that stops the agent
// re-provisioning over the user's files.
func applyImportConfig(cfg map[string]any, settings json.RawMessage) {
	if len(settings) == 0 {
		return
	}
	var s struct {
		Import *struct {
			JarFile   string `json:"jar_file"`
			NoInstall bool   `json:"no_install"`
		} `json:"import"`
	}
	if err := json.Unmarshal(settings, &s); err != nil || s.Import == nil {
		return
	}
	if s.Import.JarFile != "" {
		cfg["jar_file"] = s.Import.JarFile
	}
	if s.Import.NoInstall {
		cfg["no_install"] = true
	}
}

// HelperModMCSeries mirrors the agent's constant and the jar's own
// fabric.mod.json. All three must move together.
const HelperModMCSeries = "26.2"

// HelperModCompatible reports whether the shipped jar can load on a server.
//
// Fabric Loader treats an unsatisfied dependency as fatal, so enabling the mod
// on the wrong Minecraft version would prevent that server from starting at
// all. The agent enforces this too; checking here as well is what lets the UI
// explain why the option is unavailable instead of accepting a toggle that
// silently does nothing.
func HelperModCompatible(platform, mcVersion string) bool {
	if platform != "fabric" {
		return false
	}
	v := strings.TrimSpace(mcVersion)
	return v == HelperModMCSeries || strings.HasPrefix(v, HelperModMCSeries+".")
}

// HelperModEnabled reports whether a server should run the manager's helper mod.
//
// An explicit choice in settings always wins. With no choice recorded, the
// default is on for Fabric servers the panel created and off for imported ones:
// an imported directory is somebody's existing, possibly curated instance, and
// adding a jar to it without being asked is not ours to do.
func HelperModEnabled(settings json.RawMessage, platform, mcVersion string) bool {
	var s struct {
		HelperMod *bool           `json:"helper_mod"`
		Import    json.RawMessage `json:"import"`
	}
	if len(settings) > 0 {
		if err := json.Unmarshal(settings, &s); err == nil && s.HelperMod != nil {
			return *s.HelperMod
		}
	}

	// Only default it on where it can actually load. The agent independently
	// refuses to install onto an incompatible version, so a wrong default here
	// would be inert rather than dangerous — but it would still show the user a
	// switch that claims something untrue.
	if !HelperModCompatible(platform, mcVersion) {
		return false
	}
	return len(s.Import) == 0
}
