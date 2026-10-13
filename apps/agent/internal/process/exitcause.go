package process

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"time"
)

// Exit causes reported in StatusInfo.ExitCause.
const (
	ExitRequested      = "requested"
	ExitClean          = "exited"
	ExitCrashed        = "crashed"
	ExitStartupFailure = "startup_failure"
)

// exitLogTailBytes is how much of the end of the console log is read to tell
// a clean shutdown from a crash when the exit code alone cannot.
const exitLogTailBytes = 64 << 10

// crashMarkers appear when a server dies from an error, even if it then goes
// through its normal shutdown and exits with status 0.
var crashMarkers = []string{
	"Encountered an unexpected exception",
	"This crash report has been saved to",
	"Exception in server tick loop",
	"A mod crashed on startup",
	"Failed to start the minecraft server",
	"java.lang.OutOfMemoryError",
}

// stopMarkers appear when a server shuts itself down normally.
var stopMarkers = []string{
	"Stopping the server",
	"Stopping server",
	"All dimensions are saved",
}

// classifyExit decides how a finished run is reported. A stop the panel asked
// for is "requested" whatever the code; otherwise a crash report wins, then a
// zero exit code means the server stopped itself, and with no code at all (a
// reattached process the agent never parented) the log decides.
func classifyExit(prev Status, requested bool, code *int, tail []string) (Status, string) {
	switch {
	case prev == StatusStartupFailure:
		return StatusStartupFailure, ExitStartupFailure
	case requested || prev == StatusStopping:
		return StatusOffline, ExitRequested
	case containsAny(tail, crashMarkers):
		return StatusCrashed, ExitCrashed
	case code != nil && *code == 0:
		return StatusOffline, ExitClean
	case code == nil && containsAny(tail, stopMarkers):
		return StatusOffline, ExitClean
	default:
		return StatusCrashed, ExitCrashed
	}
}

func containsAny(lines []string, markers []string) bool {
	for _, line := range lines {
		for _, m := range markers {
			if strings.Contains(line, m) {
				return true
			}
		}
	}
	return false
}

// collectExitCode returns the process's exit code when the agent launched it,
// waiting briefly for the reaper to report, or nil when it cannot be known.
// -1 means the process was ended by a signal.
func (inst *Instance) collectExitCode() *int {
	inst.mu.RLock()
	cmd, exited := inst.cmd, inst.exitStatus
	inst.mu.RUnlock()
	if cmd != nil && cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		return &code
	}
	if exited == nil {
		return nil
	}
	select {
	case state := <-exited:
		if state == nil {
			return nil
		}
		code := state.ExitCode()
		return &code
	case <-time.After(2 * time.Second):
		return nil
	}
}

// readTail returns the lines in the last n bytes of a file.
func readTail(path string, n int64) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > n {
		if _, err := f.Seek(-n, io.SeekEnd); err != nil {
			return nil
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	return strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
}

// newRunID returns an identifier for one launch.
func newRunID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
