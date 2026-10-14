//go:build linux

package process

import (
	"os/exec"
	"testing"
	"time"
)

// A process hidden behind another user is recognised at reattach by its start
// time; a pid that started at another time (a reused pid) is not.
func TestProcessStartedAt(t *testing.T) {
	launched := time.Now()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	if !processStartedAt(cmd.Process.Pid, launched) {
		t.Fatal("the process was not recognised by its start time")
	}
	if processStartedAt(cmd.Process.Pid, launched.Add(-time.Hour)) {
		t.Fatal("a process started an hour later was taken for the recorded one")
	}
	if processStartedAt(cmd.Process.Pid, time.Time{}) {
		t.Fatal("a record without a start time matched")
	}
}
