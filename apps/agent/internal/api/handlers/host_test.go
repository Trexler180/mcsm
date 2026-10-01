package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakePower struct {
	canErr    error
	rebootErr error
	calls     []string
}

func (f *fakePower) CanReboot(context.Context) error {
	f.calls = append(f.calls, "can")
	return f.canErr
}

func (f *fakePower) Reboot(context.Context) error {
	f.calls = append(f.calls, "reboot")
	return f.rebootErr
}

type fakeStopper struct {
	power *fakePower
	stops int
}

func (s *fakeStopper) StopAll(time.Duration) {
	s.stops++
	s.power.calls = append(s.power.calls, "stop")
}

func newHostFixture(enabled bool, power *fakePower) (*HostHandlers, *fakeStopper) {
	stopper := &fakeStopper{power: power}
	var hp HostPower
	if power != nil {
		hp = power
	}
	h := NewHostHandlers(enabled, stopper, hp)
	h.async = func(f func()) { f() }
	return h, stopper
}

func reboot(h *HostHandlers) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.Reboot(rr, httptest.NewRequest(http.MethodPost, "/agent/v1/host/reboot", nil))
	return rr
}

// Without the opt-in, nothing is asked of the OS and no server is touched.
func TestRebootIsOffUnlessEnabled(t *testing.T) {
	power := &fakePower{}
	h, stopper := newHostFixture(false, power)
	if rr := reboot(h); rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", rr.Code)
	}
	if stopper.stops != 0 || len(power.calls) != 0 {
		t.Fatalf("a disabled reboot did work: stops=%d calls=%v", stopper.stops, power.calls)
	}
}

func TestRebootIsUnsupportedWithoutAPlatformImplementation(t *testing.T) {
	h, stopper := newHostFixture(true, nil)
	if rr := reboot(h); rr.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d, want 501", rr.Code)
	}
	if stopper.stops != 0 {
		t.Fatal("servers were stopped on an unsupported platform")
	}
}

// If the OS would refuse, the servers must stay up: stopping them first would
// leave the host running with every server down.
func TestRebootRefusedByTheOSLeavesServersRunning(t *testing.T) {
	power := &fakePower{canErr: errors.New(`logind answered "challenge"`)}
	h, stopper := newHostFixture(true, power)
	rr := reboot(h)
	if rr.Code != http.StatusPreconditionFailed {
		t.Fatalf("status=%d, want 412", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "polkit") {
		t.Fatalf("the refusal should say how to fix it: %s", rr.Body.String())
	}
	if stopper.stops != 0 || strings.Join(power.calls, ",") != "can" {
		t.Fatalf("a refused reboot did more than ask: stops=%d calls=%v", stopper.stops, power.calls)
	}
	// And the refusal does not wedge the in-progress flag.
	power.canErr = nil
	if rr := reboot(h); rr.Code != http.StatusAccepted {
		t.Fatalf("retry after fixing the grant: status=%d", rr.Code)
	}
}

// The happy path asks first, stops the servers second, reboots last.
func TestRebootStopsServersBeforeRebooting(t *testing.T) {
	power := &fakePower{}
	h, _ := newHostFixture(true, power)
	if rr := reboot(h); rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d, want 202", rr.Code)
	}
	if got := strings.Join(power.calls, ","); got != "can,stop,reboot" {
		t.Fatalf("order=%s, want can,stop,reboot", got)
	}
	// Once a reboot is under way, a second request is refused rather than
	// stopping servers again.
	if rr := reboot(h); rr.Code != http.StatusConflict {
		t.Fatalf("second request: status=%d, want 409", rr.Code)
	}
}

// A reboot that fails after the servers stopped must not block a retry.
func TestFailedRebootCanBeRetried(t *testing.T) {
	power := &fakePower{rebootErr: errors.New("inhibited")}
	h, _ := newHostFixture(true, power)
	if rr := reboot(h); rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d", rr.Code)
	}
	power.rebootErr = nil
	if rr := reboot(h); rr.Code != http.StatusAccepted {
		t.Fatalf("retry: status=%d, want 202", rr.Code)
	}
}

func TestParseCanReboot(t *testing.T) {
	if err := parseCanReboot("s \"yes\"\n"); err != nil {
		t.Fatalf("yes was refused: %v", err)
	}
	for _, reply := range []string{`s "no"`, `s "challenge"`, `s "na"`, ``, `garbage`} {
		if err := parseCanReboot(reply); err == nil {
			t.Errorf("%q was accepted", reply)
		}
	}
}
