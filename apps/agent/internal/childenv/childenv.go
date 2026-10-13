// Package childenv builds the environment for processes the agent starts on
// someone else's behalf: Minecraft servers, runtime installers, and Java
// probes. All of them run third-party code (mods, plugins, installer jars), so
// they get an allowlist of the variables a JVM actually needs and never the
// agent's own environment, which holds AGENT_TOKEN — the credential that
// controls every server on the node.
package childenv

import (
	"os"
	"runtime"
	"strings"
)

// PassthroughVar names extra variables an operator wants handed to servers
// (comma-separated), for setups that rely on one — a proxy setting, a license
// key a plugin reads. Secret-shaped names are refused even when listed.
const PassthroughVar = "AGENT_CHILD_ENV_PASSTHROUGH"

// allowed lists the variables a JVM, its launcher scripts, and the installers
// need to find themselves, resolve paths, and format text. Anything else in the
// agent's environment stays behind.
var allowed = []string{
	"PATH", "HOME", "USER", "LOGNAME", "LANG", "LANGUAGE", "TZ", "TMPDIR",
	"JAVA_HOME",
	// Windows: the JVM cannot start without some of these, and the rest keep
	// user-profile paths (temp, app data) resolving the way they do for the
	// agent itself.
	"SYSTEMROOT", "WINDIR", "SYSTEMDRIVE", "COMSPEC", "PATHEXT", "TEMP", "TMP",
	"USERPROFILE", "USERNAME", "HOMEDRIVE", "HOMEPATH", "APPDATA", "LOCALAPPDATA",
	"PROGRAMDATA", "PROGRAMFILES", "PROGRAMFILES(X86)", "PROGRAMW6432",
	"COMMONPROGRAMFILES", "COMMONPROGRAMFILES(X86)", "NUMBER_OF_PROCESSORS",
	"PROCESSOR_ARCHITECTURE", "PROCESSOR_IDENTIFIER", "OS",
}

// allowedPrefixes admits whole families, such as locale categories.
var allowedPrefixes = []string{"LC_"}

// refusedPrefixes can never be passed through, however the operator configures
// it: the agent's own settings (which include its token) and the helper-mod
// link variables, which are injected explicitly per launch.
var refusedPrefixes = []string{"AGENT_", "MCSM_"}

// refused names secrets the API or agent may hold in a shared environment file.
var refused = map[string]bool{
	"JWT_SECRET": true, "APP_ENCRYPTION_KEY": true, "APP_ENCRYPTION_KEY_FILE": true,
	"ADMIN_PASSWORD": true, "LOCAL_AGENT_TOKEN": true, "CURSEFORGE_API_KEY": true,
}

// Build returns the environment for a child process: the allowlisted part of the
// agent's environment, plus any operator passthrough, plus extra (KEY=VALUE
// pairs that win over anything inherited). It never returns nil, so an
// exec.Cmd given this value never falls back to inheriting everything.
func Build(extra ...string) []string {
	return build(os.Environ(), os.Getenv(PassthroughVar), extra)
}

func build(environ []string, passthrough string, extra []string) []string {
	keep := map[string]bool{}
	for _, name := range allowed {
		keep[normalize(name)] = true
	}
	for _, name := range strings.Split(passthrough, ",") {
		name = strings.TrimSpace(name)
		if name != "" && !isRefused(name) {
			keep[normalize(name)] = true
		}
	}

	overridden := map[string]bool{}
	for _, kv := range extra {
		if name, _, ok := strings.Cut(kv, "="); ok {
			overridden[normalize(name)] = true
		}
	}

	out := make([]string, 0, len(keep)+len(extra))
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		key := normalize(name)
		if overridden[key] || isRefused(name) {
			continue
		}
		if keep[key] || hasAllowedPrefix(key) {
			out = append(out, kv)
		}
	}
	return append(out, extra...)
}

func isRefused(name string) bool {
	upper := strings.ToUpper(name)
	if refused[upper] {
		return true
	}
	for _, p := range refusedPrefixes {
		if strings.HasPrefix(upper, p) {
			return true
		}
	}
	return false
}

func hasAllowedPrefix(key string) bool {
	for _, p := range allowedPrefixes {
		if strings.HasPrefix(key, normalize(p)) {
			return true
		}
	}
	return false
}

// normalize folds case on Windows, where environment names are
// case-insensitive (Path and PATH are the same variable).
func normalize(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}
