//go:build windows

package process

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// supportsReattach is false on Windows: local dev has no systemd and no
// deploy-driven agent restarts, so servers stay tied to the agent's lifetime.
const supportsReattach = false

// launch starts the JVM as a child process with stdout+stderr redirected to the
// console log (so the agent tails one file on every platform) and stdin via a
// pipe. The *exec.Cmd is retained so exit is detected with cmd.Wait.
func launch(java string, args []string, dir, logPath, fifoPath string) (*launched, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open console log: %w", err)
	}
	cmd := exec.Command(java, args...)
	cmd.Dir = dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		logFile.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		stdin.Close()
		return nil, fmt.Errorf("start process: %w", err)
	}
	logFile.Close()
	return &launched{pid: cmd.Process.Pid, pgid: cmd.Process.Pid, stdin: stdin, cmd: cmd}, nil
}

// openStdinWriter is unsupported on Windows (no reattach), so it is never called
// on the live path; it exists to satisfy the cross-platform symbol set.
func openStdinWriter(fifoPath string) (io.WriteCloser, error) {
	return nil, fmt.Errorf("reattach not supported on windows")
}

func processAlive(pid int) bool { return false }

func processMatchesDir(pid int, dir string) bool { return false }

// terminate kills the process. Windows has no cheap process-group kill here; the
// single-process kill matches the previous behavior and suffices for local dev.
func terminate(pid, pgid int, force bool) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
