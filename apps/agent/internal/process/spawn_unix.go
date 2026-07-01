//go:build !windows

package process

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// supportsReattach is true on platforms where servers are spawned detached and
// can be re-adopted after an agent restart.
const supportsReattach = true

// launch starts the JVM detached from the agent: a new session (Setsid) so it
// leaves the agent's process group, stdout+stderr redirected straight to the
// console log file (no agent goroutine in the path, so output survives the agent
// dying), and stdin connected to a FIFO opened O_RDWR (so the server never sees
// stdin EOF while no agent is attached). We Release the process and track it by
// PID, so the same exit/stop machinery serves both fresh starts and reattaches.
func launch(java string, args []string, dir, logPath, fifoPath string) (*launched, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open console log: %w", err)
	}
	stdin, err := openStdinWriter(fifoPath)
	if err != nil {
		logFile.Close()
		return nil, err
	}

	cmd := exec.Command(java, args...)
	cmd.Dir = dir
	cmd.Stdin = stdin.(*os.File) // passed as a real fd, so the child reads the FIFO directly
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// New session => the child is a process-group leader detached from the
	// agent's controlling terminal and group, so it isn't taken down with us.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		stdin.Close()
		return nil, fmt.Errorf("start process: %w", err)
	}
	pid := cmd.Process.Pid
	// The child has dup'd the log fd; the agent doesn't write it. Keep the FIFO
	// fd (stdin) open — that's how we send console commands.
	logFile.Close()

	// Reap the child in the background once it exits. Setsid only detaches the
	// session/process group — this agent process remains the child's real OS
	// parent, so nothing else will ever wait() on this pid. Previously we called
	// cmd.Process.Release() here, which stops Go from tracking the process but
	// does NOT reap it: a dead child sits as a zombie in the process table
	// forever. Zombies still answer kill(pid, 0) as "alive", so watchExit's
	// liveness poll (processAlive) never observed the exit, finalize() never
	// ran, and the instance got stuck reporting "stopping" forever — which also
	// blocked Manager.Start from ever starting the server again. Waiting here
	// (in a goroutine, so it doesn't block launch/reattach) fixes that while
	// keeping cmd nil in `launched`, so watchExit still uses the PID-polling
	// path (needed for reattach after an agent restart, where the new agent
	// process isn't the child's parent and can't Wait() on it anyway).
	go func() { _, _ = cmd.Process.Wait() }()

	return &launched{pid: pid, pgid: pid, stdin: stdin, cmd: nil}, nil
}

// openStdinWriter creates the FIFO if needed and opens it O_RDWR. Opening
// read+write means the FIFO always has at least one writer (us, plus the child's
// inherited fd), so neither the child nor a reattaching agent ever hits EOF.
func openStdinWriter(fifoPath string) (io.WriteCloser, error) {
	if err := os.MkdirAll(filepath.Dir(fifoPath), 0o755); err != nil {
		return nil, err
	}
	if _, err := os.Stat(fifoPath); os.IsNotExist(err) {
		if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
			return nil, fmt.Errorf("mkfifo: %w", err)
		}
	}
	f, err := os.OpenFile(fifoPath, os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open fifo: %w", err)
	}
	return f, nil
}

// processAlive reports whether pid names a live process (signal 0 probes
// existence without delivering a signal; EPERM still means it exists).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// processMatchesDir guards against PID reuse at reattach time: the candidate
// process must have the recorded server directory as its working directory
// (every server is launched with cmd.Dir = its directory).
func processMatchesDir(pid int, dir string) bool {
	link, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return false
	}
	got, err := filepath.EvalSymlinks(link)
	if err != nil {
		got = link
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		want = dir
	}
	return got == want
}

// terminate signals the process group (negative pid) so child helper processes
// die too, falling back to the single process if the group is already gone.
func terminate(pid, pgid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	target := pgid
	if target <= 0 {
		target = pid
	}
	if err := syscall.Kill(-target, sig); err != nil {
		return syscall.Kill(pid, sig)
	}
	return nil
}
