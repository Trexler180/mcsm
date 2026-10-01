package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"time"
)

// HostPower is the privileged half of a host reboot, kept behind an interface
// so the handler's ordering and guards can be tested without rebooting the
// machine running the tests.
type HostPower interface {
	// CanReboot asks the OS whether this process may reboot the host right
	// now, without doing it. Anything other than nil means "no".
	CanReboot(ctx context.Context) error
	// Reboot asks the OS to reboot. It returns once the request is accepted.
	Reboot(ctx context.Context) error
}

// serverStopper is the one Manager method a reboot needs.
type serverStopper interface {
	StopAll(timeout time.Duration)
}

// HostHandlers serves host-level operations. Today that is only a reboot.
//
// Rebooting is off unless the operator opts in with AGENT_ALLOW_REBOOT=1: the
// agent token already controls every server on the host, but taking the whole
// machine down is a different kind of authority, and a deployment that never
// asked for it should not have it. The OS is the second gate — the agent runs
// unprivileged, so the host must also grant it the reboot action (a polkit
// rule; see docs/deployment.md), and the handler checks that grant before it
// touches a single server.
type HostHandlers struct {
	enabled     bool
	power       HostPower
	servers     serverStopper
	stopTimeout time.Duration
	inProgress  atomic.Bool
	// async runs the stop-then-reboot sequence. Tests replace it to run inline.
	async func(func())
}

func NewHostHandlers(enabled bool, servers serverStopper, power HostPower) *HostHandlers {
	return &HostHandlers{
		enabled:     enabled,
		power:       power,
		servers:     servers,
		stopTimeout: 60 * time.Second,
		async:       func(f func()) { go f() },
	}
}

// Reboot stops every Minecraft server gracefully and then reboots the host.
//
// The order of checks is the point: everything that can refuse — the opt-in,
// the platform, a reboot already under way, and the OS's own permission — is
// settled before any server is stopped, so a refused reboot never leaves the
// host up with its servers down. Only then does it answer 202 and do the slow
// part in the background; the caller learns the outcome by the node going
// offline and coming back.
func (h *HostHandlers) Reboot(w http.ResponseWriter, r *http.Request) {
	if !h.enabled {
		writeError(w, http.StatusForbidden, "host reboot is disabled on this agent (set AGENT_ALLOW_REBOOT=1 to enable it)")
		return
	}
	if h.power == nil {
		writeError(w, http.StatusNotImplemented, "host reboot is not supported on this platform")
		return
	}
	if !h.inProgress.CompareAndSwap(false, true) {
		writeError(w, http.StatusConflict, "a reboot is already in progress")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := h.power.CanReboot(ctx); err != nil {
		h.inProgress.Store(false)
		log.Printf("host reboot refused by the OS: %v", err)
		writeError(w, http.StatusPreconditionFailed,
			"the host does not allow this agent to reboot it; install the polkit rule from docs/deployment.md")
		return
	}

	log.Printf("host reboot requested: stopping all servers (up to %s each), then rebooting", h.stopTimeout)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "rebooting"})

	h.async(func() {
		h.servers.StopAll(h.stopTimeout)
		rctx, rcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer rcancel()
		if err := h.power.Reboot(rctx); err != nil {
			// The servers are already stopped; say so loudly, and allow another
			// attempt rather than wedging the flag.
			log.Printf("host reboot FAILED after stopping servers: %v", err)
			h.inProgress.Store(false)
		}
	})
}

// SystemdPower reboots through systemd-logind, which is what lets an
// unprivileged service do it at all: logind consults polkit, and the operator's
// rule decides. Nothing here uses sudo or a shell, and both commands have fixed
// arguments.
type SystemdPower struct{}

// NewHostPower returns the platform's implementation, or nil where host reboot
// is unsupported.
func NewHostPower() HostPower {
	if runtime.GOOS != "linux" {
		return nil
	}
	return SystemdPower{}
}

// CanReboot asks logind the same question a reboot would, without rebooting.
// Only a plain "yes" counts: "challenge" means polkit would want a password
// this service cannot give, and "no"/"na" are refusals.
func (SystemdPower) CanReboot(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "busctl", "call",
		"org.freedesktop.login1", "/org/freedesktop/login1",
		"org.freedesktop.login1.Manager", "CanReboot").Output()
	if err != nil {
		return fmt.Errorf("asking logind: %w", err)
	}
	return parseCanReboot(string(out))
}

func (SystemdPower) Reboot(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "systemctl", "reboot").CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl reboot: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// parseCanReboot reads busctl's reply to CanReboot, which looks like `s "yes"`.
func parseCanReboot(reply string) error {
	answer := strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(reply), "s")), `"`)
	if answer == "yes" {
		return nil
	}
	if answer == "" {
		return errors.New("logind gave no answer")
	}
	return fmt.Errorf("logind answered %q", answer)
}
