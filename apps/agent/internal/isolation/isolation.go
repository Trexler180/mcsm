// Package isolation runs Minecraft servers — and the installers that build
// them — as a dedicated server user instead of the agent's own.
//
// Everything a server loads (every mod and plugin) runs in its JVM. Sharing a
// user with the agent and the API gave that code the panel's database, its
// encryption key, and every other server's launch token. Isolated, a server
// runs as the server user (default mcsm-mc) through the mcsm-launch helper,
// which can switch to that user and no other; it can still reach its own
// directory, which the agent shares with it through the server user's group.
//
// AGENT_ISOLATE_SERVERS chooses the mode:
//
//	unset / auto  isolate when the host is set up for it, otherwise run servers
//	              as the agent's user and say why (the default)
//	1 / enforce   isolate, and refuse to start servers when the host is not set
//	              up for it (what a provisioned host uses)
//	0 / off       never isolate: servers run as the agent's user, with whatever
//	              that user can reach — the deliberate opt-out
//
// AGENT_SERVER_USER names the user (default mcsm-mc) and AGENT_LAUNCHER the
// helper (default /usr/local/libexec/mcsm/mcsm-launch). Only Linux isolates.
package isolation

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
)

// Mode is the configured isolation policy.
type Mode string

const (
	Auto     Mode = "auto"
	Enforced Mode = "enforced"
	Off      Mode = "off"
)

const (
	DefaultUser     = "mcsm-mc"
	DefaultLauncher = "/usr/local/libexec/mcsm/mcsm-launch"
)

// Config is where isolation comes from.
type Config struct {
	Mode       Mode
	User       string
	Launcher   string
	ServerRoot string
}

// ParseMode reads AGENT_ISOLATE_SERVERS.
func ParseMode(v string) Mode {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on", "enforce", "enforced":
		return Enforced
	case "0", "false", "no", "off", "disable", "disabled":
		return Off
	default:
		return Auto
	}
}

// FromEnv reads the configuration from the agent's environment.
func FromEnv(serverRoot string) Config {
	cfg := Config{
		Mode:       ParseMode(os.Getenv("AGENT_ISOLATE_SERVERS")),
		User:       strings.TrimSpace(os.Getenv("AGENT_SERVER_USER")),
		Launcher:   strings.TrimSpace(os.Getenv("AGENT_LAUNCHER")),
		ServerRoot: serverRoot,
	}
	if cfg.User == "" {
		cfg.User = DefaultUser
	}
	if cfg.Launcher == "" {
		cfg.Launcher = DefaultLauncher
	}
	return cfg
}

// Status is what the agent reports about isolation.
type Status struct {
	Mode   Mode   `json:"mode"`
	Active bool   `json:"active"`
	User   string `json:"user,omitempty"`
	// Reason says why servers are not isolated, when they are not.
	Reason string `json:"reason,omitempty"`
}

// ErrUnavailable refuses a start when isolation is enforced but the host is
// not set up for it.
var ErrUnavailable = errors.New("server isolation is required (AGENT_ISOLATE_SERVERS=1) but unavailable")

type state struct {
	cfg    Config
	status Status
	uid    int
	gid    int
}

var (
	mu      sync.RWMutex
	current = &state{status: Status{Mode: Off, Reason: "not configured"}}
)

// Configure resolves the configuration against the host once, at startup, and
// makes it the one every launch uses.
func Configure(cfg Config) Status {
	st := resolve(cfg)
	mu.Lock()
	current = st
	mu.Unlock()
	return st.status
}

// Current reports the isolation in effect.
func Current() Status {
	mu.RLock()
	defer mu.RUnlock()
	return current.status
}

func snapshot() *state {
	mu.RLock()
	defer mu.RUnlock()
	return current
}

// Command returns how to run program with args in dir: unchanged when servers
// are not isolated, or through the launcher (after sharing dir with the
// server user) when they are. It fails when isolation is enforced and
// unavailable.
func Command(program string, args []string, dir string) (string, []string, error) {
	st := snapshot()
	if !st.status.Active {
		if st.status.Mode == Enforced {
			return "", nil, errors.Join(ErrUnavailable, errors.New(st.status.Reason+
				"; run Deploy-Dashboard.ps1 -Provision, or set AGENT_ISOLATE_SERVERS=0 to run servers as the agent's user"))
		}
		return program, args, nil
	}
	return wrap(st, program, args, dir)
}

// Reclaim gives the agent back what the server user wrote under dir and kept
// to itself. The launcher's umask shares most of what a server creates, but
// Java writes some files through a temporary file it creates owner-only and
// renames into place (Minecraft saves level.dat and player data that way), so
// the agent, reading through the group, is shut out of the files that matter
// most to backups. Reclaim runs a chmod as the server user, through the
// launcher: it can only touch files that user owns, which the server could
// change anyway. It does nothing when servers are not isolated.
func Reclaim(dir string) error {
	st := snapshot()
	if !st.status.Active {
		return nil
	}
	return reclaimFn(st, dir)
}

// reclaimFn is reclaim, replaceable in tests.
var reclaimFn = reclaim

// RetryDenied runs op and, when it fails for lack of permission while servers
// are isolated, reclaims dir and runs it once more. It suits an operation that
// is safe to repeat: a read, or a write that replaces a whole file. A stream
// that may already have sent part of its output should call Reclaim first
// instead.
func RetryDenied(dir string, op func() error) error {
	err := op()
	if err == nil || !errors.Is(err, fs.ErrPermission) || !Current().Active {
		return err
	}
	// Retry even when the reclaim reports a problem: it is best effort (a file
	// the server renames mid-walk fails its chmod) and has usually fixed the
	// file this op needs. Its error only explains a retry that fails too.
	rerr := Reclaim(dir)
	if err := op(); err != nil {
		return errors.Join(err, rerr)
	}
	return nil
}
