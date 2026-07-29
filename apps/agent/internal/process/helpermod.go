package process

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mcsm/agent/internal/helperjar"
	"github.com/mcsm/agent/internal/helpermod"
)

// The helper mod is shipped inside the agent binary and installed into a
// server's mods directory on start, so enabling it in the panel is all a user
// has to do — there is no download step and no manual file copying.
//
// Two rules shape this code:
//
//  1. It is opt-in per server, and turning it off must actually remove the jar.
//     Leaving a disabled mod on disk would be a lie, and the next start would
//     silently reinstate it.
//  2. Failing to install must never prevent a server from starting. Telemetry is
//     worth less than someone's server coming up, so every failure here is
//     reported as a console note and otherwise ignored.

// helperModRelPath is where the jar lives inside a server directory.
func helperModRelPath() string {
	return filepath.Join("mods", helperjar.FileName)
}

// ensureHelperMod reconciles the helper jar in a server directory against
// whether the server wants it.
//
// Returns a human-readable note for the console when something changed or went
// wrong, and an empty string when there was nothing to say.
func ensureHelperMod(dir, platform, mcVersion string, enabled bool, resolver *helpermod.Resolver) string {
	target := filepath.Join(dir, helperModRelPath())

	if !enabled {
		// Remove rather than leave a stale copy: a user who disabled the mod
		// expects it gone, and Fabric would otherwise keep loading it.
		if err := os.Remove(target); err != nil {
			if os.IsNotExist(err) {
				return ""
			}
			return "[mcsm] warning: could not remove helper mod: " + err.Error()
		}
		return "[mcsm] helper mod removed (disabled for this server)"
	}

	if !supportsHelperMod(dir, platform) {
		// Not an error: the mod is Fabric-only for now, and a Paper or vanilla
		// server simply keeps using the log-scraping path.
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), helperResolveTimeout)
	defer cancel()

	resolved, err := helperResolver(resolver).Resolve(ctx, "fabric", mcVersion)

	switch {
	case err != nil:
		// We could not determine the right build — a network blip, a bad index.
		// Deliberately leave whatever is installed alone: deleting a working mod
		// because DNS hiccuped would be a far worse outcome than running a build
		// that is one release behind.
		return "[mcsm] warning: could not check for a helper mod build: " + err.Error()

	case resolved == nil:
		// Authoritatively nothing supports this version. Any installed jar is now
		// wrong for this server, and Fabric treats an unsatisfied dependency as
		// fatal — so a leftover jar would stop the server from booting.
		if _, statErr := os.Stat(target); statErr == nil {
			if err := os.Remove(target); err != nil {
				return "[mcsm] warning: could not remove incompatible helper mod: " + err.Error()
			}
			return "[mcsm] helper mod removed (no build available for Minecraft " + describeVersion(mcVersion) + ")"
		}
		return "[mcsm] helper mod skipped (no build available for Minecraft " + describeVersion(mcVersion) + ")"
	}

	current, err := fileSHA256(target)
	if err == nil && current == resolved.SHA256 {
		return "" // Already current; nothing to do and nothing to say.
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "[mcsm] warning: could not create mods directory: " + err.Error()
	}

	if err := writeFileAtomic(target, resolved.Data, 0o644); err != nil {
		return "[mcsm] warning: could not install helper mod: " + err.Error()
	}

	action := "installed"
	if current != "" {
		action = "updated to"
	}
	return fmt.Sprintf("[mcsm] helper mod %s %s for Minecraft %s (%s)",
		action, describeModVersion(resolved.ModVersion), describeVersion(mcVersion), resolved.Source)
}

// helperResolveTimeout bounds the whole resolve, including any download. A
// server start must not hang on a slow mirror.
const helperResolveTimeout = 45 * time.Second

// HelperModResolver is the resolver used for server starts. Set once at agent
// startup. Nil falls back to embedded-only resolution, which is what tests and
// any caller without network configuration get.
var HelperModResolver *helpermod.Resolver

func helperResolver(r *helpermod.Resolver) *helpermod.Resolver {
	if r != nil {
		return r
	}
	if HelperModResolver != nil {
		return HelperModResolver
	}
	return EmbeddedOnlyResolver()
}

// EmbeddedOnlyResolver serves just the build compiled into this binary.
func EmbeddedOnlyResolver() *helpermod.Resolver {
	return &helpermod.Resolver{
		MaxProtocol: helperjar.ProtocolVersion,
		Fallback: helpermod.Fallback{
			Data:              helperjar.Bytes(),
			ModVersion:        helperjar.ModVersion,
			MinecraftVersions: helperjar.MinecraftVersions,
			ProtocolVersion:   helperjar.ProtocolVersion,
		},
	}
}

func describeModVersion(v string) string {
	if strings.TrimSpace(v) == "" {
		return "an unknown build"
	}
	return "v" + v
}

func describeVersion(mcVersion string) string {
	if strings.TrimSpace(mcVersion) == "" {
		return "an unknown version"
	}
	return mcVersion
}

// supportsHelperMod reports whether this server can load the jar.
//
// The declared platform is trusted when present; imported servers often have no
// platform recorded, so the directory is sniffed for Fabric's markers instead.
func supportsHelperMod(dir, platform string) bool {
	if platform == "fabric" {
		return true
	}
	if platform != "" {
		// A server that declares itself as something else is not Fabric, and
		// dropping a Fabric mod into it would at best be ignored and at worst
		// confuse a loader that scans mods/.
		return false
	}

	for _, marker := range []string{"fabric-server-launch.jar", "fabric-server-launcher.jar"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	if info, err := os.Stat(filepath.Join(dir, "libraries", "net", "fabricmc")); err == nil && info.IsDir() {
		return true
	}
	return false
}

// IsManagedMod reports whether a jar in a server's mods directory is the one the
// manager installs.
//
// Mod reconciliation uses this to avoid adopting the helper as a user-installed
// mod — otherwise the panel would offer to update it against Modrinth, where it
// does not exist, and let someone delete it without turning the feature off.
func IsManagedMod(fileName string) bool {
	return filepath.Base(fileName) == helperjar.FileName
}

// HelperModSHA256 exposes the embedded jar's digest for callers that want to
// report which build is installed.
func HelperModSHA256() string {
	return helperjar.SHA256()
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// writeFileAtomic writes via a temp file and rename so a crash or a concurrent
// server start can never observe a half-written jar — which Fabric would refuse
// to load, taking the server down with it.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
