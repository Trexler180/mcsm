//go:build !windows

package process

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mcsm/agent/internal/childenv"
	"github.com/mcsm/agent/internal/isolation"
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
// extraEnv entries (KEY=VALUE) are appended to the agent's own environment; they
// carry the helper mod's link credentials. Nil means the mod stays dormant.
func launch(java string, args []string, dir, logPath, fifoPath string, extraEnv []string) (*launched, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open console log: %w", err)
	}
	stdin, err := openStdinWriter(fifoPath)
	if err != nil {
		logFile.Close()
		return nil, err
	}

	// Isolated, the JVM is started by the launcher as the server user; the
	// launcher execs it in place, so the pid tracked below is the JVM's.
	program, argv, err := isolation.Command(java, args, dir)
	if err != nil {
		logFile.Close()
		stdin.Close()
		return nil, err
	}
	cmd := exec.Command(program, argv...)
	cmd.Dir = dir
	// Always an explicit environment: a nil Env would inherit the agent's,
	// including AGENT_TOKEN, and hand it to every mod and plugin.
	cmd.Env = childenv.Build(extraEnv...)
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
	//
	// The exit status goes to exited, so the instance can tell a clean exit
	// (status 0) from a crash. A reattached process has no such channel.
	exited := make(chan *os.ProcessState, 1)
	go func() {
		state, _ := cmd.Process.Wait()
		exited <- state
	}()

	return &launched{pid: pid, pgid: pid, stdin: stdin, cmd: nil, exited: exited}, nil
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
// (every server is launched with cmd.Dir = its directory). A server running as
// the isolated server user hides its working directory from the agent; it is
// then identified by when it started instead.
func processMatchesDir(pid int, dir string, startedAt time.Time) bool {
	link, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if errors.Is(err, fs.ErrPermission) {
		return processStartedAt(pid, startedAt)
	}
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

// startSlack is how far a process's start time may be from the time the agent
// recorded for it: the record is written just after the launch.
const startSlack = 10 * time.Second

// processStartedAt reports whether pid started at about the recorded time,
// read from /proc/<pid>/stat (readable for any user's process) and the boot
// time. A reused pid belongs to a process started later.
func processStartedAt(pid int, startedAt time.Time) bool {
	if startedAt.IsZero() {
		return false
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	// Fields after the parenthesised command name; starttime is field 22.
	i := strings.LastIndexByte(string(stat), ')')
	if i < 0 {
		return false
	}
	fields := strings.Fields(string(stat[i+1:]))
	if len(fields) < 20 {
		return false
	}
	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return false
	}
	boot, ok := bootTime()
	if !ok {
		return false
	}
	started := boot.Add(time.Duration(ticks) * time.Second / clockTicks)
	diff := started.Sub(startedAt)
	return diff > -startSlack && diff < startSlack
}

// clockTicks is USER_HZ, which Linux fixes at 100 for /proc.
const clockTicks = 100

func bootTime() (time.Time, bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			sec, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return time.Time{}, false
			}
			return time.Unix(sec, 0), true
		}
	}
	return time.Time{}, false
}
