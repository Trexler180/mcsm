package process

import "testing"

func intp(v int) *int { return &v }

func TestClassifyExit(t *testing.T) {
	crashTail := []string{"[12:00:00] [Server thread/ERROR]: Encountered an unexpected exception", "Stopping server"}
	stopTail := []string{"[12:00:00] [Server thread/INFO]: Stopping the server", "All dimensions are saved"}
	for _, tc := range []struct {
		name       string
		prev       Status
		requested  bool
		code       *int
		tail       []string
		wantStatus Status
		wantCause  string
	}{
		{"a stop the panel asked for", StatusStopping, true, intp(143), nil, StatusOffline, ExitRequested},
		{"a kill the panel asked for", StatusOnline, true, intp(-1), nil, StatusOffline, ExitRequested},
		{"a diagnosed startup failure", StatusStartupFailure, false, intp(1), nil, StatusStartupFailure, ExitStartupFailure},
		{"/stop typed in game", StatusOnline, false, intp(0), stopTail, StatusOffline, ExitClean},
		{"a crash report wins over a zero code", StatusOnline, false, intp(0), crashTail, StatusCrashed, ExitCrashed},
		{"a non-zero exit", StatusOnline, false, intp(1), nil, StatusCrashed, ExitCrashed},
		{"killed by a signal nobody asked for", StatusOnline, false, intp(-1), nil, StatusCrashed, ExitCrashed},
		{"reattached, clean shutdown in the log", StatusOnline, false, nil, stopTail, StatusOffline, ExitClean},
		{"reattached, nothing to go on", StatusOnline, false, nil, nil, StatusCrashed, ExitCrashed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, cause := classifyExit(tc.prev, tc.requested, tc.code, tc.tail)
			if status != tc.wantStatus || cause != tc.wantCause {
				t.Fatalf("= (%s, %s), want (%s, %s)", status, cause, tc.wantStatus, tc.wantCause)
			}
		})
	}
}

// A run's identity and end are visible in its status, so the panel can tell a
// crash from a stop and a restart from the same process.
func TestStatusCarriesRunAndExit(t *testing.T) {
	inst := newInstance(t.TempDir(), "srv", StartConfig{})
	inst.runID = "run-1"
	inst.mu.Lock()
	inst.status = StatusOnline
	inst.stopRequested = true
	inst.mu.Unlock()
	inst.finalize()
	info := inst.statusInfo()
	if info.RunID != "run-1" || info.ExitCause != ExitRequested || info.ExitedAt == nil || info.Status != StatusOffline {
		t.Fatalf("status after a requested stop = %+v", info)
	}
}
