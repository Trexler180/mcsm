//go:build linux

package isolation

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// capKill is CAP_KILL's bit: the agent must be able to signal a server that
// runs as another user.
const capKill = 5

func resolve(cfg Config) *state {
	st := &state{cfg: cfg, status: Status{Mode: cfg.Mode, User: cfg.User}}
	if cfg.Mode == Off {
		st.status.User = ""
		st.status.Reason = "turned off (AGENT_ISOLATE_SERVERS=0): servers run as the agent's own user"
		return st
	}
	fail := func(format string, a ...any) *state {
		st.status.Reason = fmt.Sprintf(format, a...)
		return st
	}

	if err := checkLauncher(cfg.Launcher); err != nil {
		return fail("%v", err)
	}
	u, err := user.Lookup(cfg.User)
	if err != nil {
		return fail("the server user %q does not exist", cfg.User)
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil || uid == 0 || gid == 0 {
		return fail("the server user %q is not usable", cfg.User)
	}
	if uid == os.Getuid() {
		return fail("the server user %q is the agent's own user", cfg.User)
	}
	groups, _ := os.Getgroups()
	if os.Getgid() != gid && !slices.Contains(groups, gid) {
		return fail("the agent's user is not in the %q group, so it could not manage the servers' files (restart the agent after adding it)", cfg.User)
	}
	if !hasCapability(capKill) {
		return fail("the agent lacks CAP_KILL, so it could not stop a server running as %q", cfg.User)
	}
	if cfg.ServerRoot != "" {
		if err := shareRoot(cfg.ServerRoot, gid); err != nil {
			return fail("%v", err)
		}
		if path, ok := traversable(filepath.Dir(cfg.ServerRoot), uid, gid); !ok {
			return fail("the server user cannot reach %s (it needs execute permission on %s)", cfg.ServerRoot, path)
		}
	}
	st.uid, st.gid = uid, gid
	st.status.Active = true
	return st
}

// checkLauncher requires the helper to be root's, unwritable by anyone else,
// executable by the agent, and to carry its file capabilities.
func checkLauncher(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("the launcher %s is not installed", path)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || !ok || st.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("the launcher %s must be a regular file owned by root and writable only by root", path)
	}
	if unix.Access(path, unix.X_OK) != nil {
		return fmt.Errorf("the agent may not execute the launcher %s", path)
	}
	if n, err := unix.Getxattr(path, "security.capability", make([]byte, 64)); err != nil || n == 0 {
		return fmt.Errorf("the launcher %s has no file capabilities (setcap cap_setuid,cap_setgid+ep)", path)
	}
	return nil
}

// hasCapability reads the agent's effective capability set.
func hasCapability(bit uint) bool {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "CapEff:"); ok {
			mask, err := strconv.ParseUint(strings.TrimSpace(v), 16, 64)
			return err == nil && mask&(1<<bit) != 0
		}
	}
	return false
}

// shareRoot lets the server user's group into the server root (to reach its
// own directory) without letting it list or read anything else there.
func shareRoot(root string, gid int) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	st := info.Sys().(*syscall.Stat_t)
	if int(st.Uid) != os.Getuid() {
		return nil // not ours to change; traversable() decides if it works
	}
	if int(st.Gid) != gid {
		if err := os.Lchown(root, -1, gid); err != nil {
			return fmt.Errorf("share %s with the server user's group: %w", root, err)
		}
	}
	if info.Mode().Perm()&0o010 == 0 {
		return os.Chmod(root, info.Mode().Perm()|0o010)
	}
	return nil
}

// traversable reports whether the server user can walk down to dir, and the
// first directory that stops it when it cannot.
func traversable(dir string, uid, gid int) (string, bool) {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		info, err := os.Stat(d)
		if err != nil {
			return d, false
		}
		st := info.Sys().(*syscall.Stat_t)
		perm := info.Mode().Perm()
		ok := perm&0o001 != 0 || (int(st.Gid) == gid && perm&0o010 != 0) || (int(st.Uid) == uid && perm&0o100 != 0)
		if !ok {
			return d, false
		}
		if d == filepath.Dir(d) {
			return "", true
		}
	}
}

func wrap(st *state, program string, args []string, dir string) (string, []string, error) {
	if dir != "" {
		if err := ShareDir(dir, st.gid); err != nil {
			return "", nil, fmt.Errorf("share %s with the server user: %w", dir, err)
		}
	}
	path := program
	if !strings.Contains(program, "/") {
		resolved, err := exec.LookPath(program)
		if err != nil {
			return "", nil, err
		}
		path = resolved
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return st.cfg.Launcher, append([]string{"--", path}, args...), nil
}

// skipShare names directories that stay the agent's alone, should one sit
// inside a server directory.
var skipShare = map[string]bool{".mcsm-run": true, "mcsm-backups": true}

// ShareDir gives the server user's group read and write access to everything
// the agent owns under dir — files the agent installed or uploaded, a world it
// restored — and makes new directories inherit the group. What the server
// creates itself is already shared through its umask.
func ShareDir(dir string, gid int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	me := os.Getuid()
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are left as they are
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() && path != dir && skipShare[d.Name()] {
			return filepath.SkipDir
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil // devices, sockets, FIFOs: not the agent's to share
		}
		return shareEntry(path, d.IsDir(), me, gid)
	})
}

// shareEntry gives the group access to one entry the agent owns. It works on an
// open descriptor rather than the path: the server's code can rename entries in
// its directory, and a symlink swapped in between a check and a path-based
// chmod would hand the group whatever the link pointed at (the panel's
// secrets). O_NOFOLLOW refuses a link outright, and the checks and changes then
// all apply to the one file that was opened.
func shareEntry(path string, dir bool, me, gid int) error {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	if dir {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Open(path, flags, 0)
	if err != nil {
		return nil // gone, swapped for a link, or unreadable: leave it
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil
	}
	isDir := st.Mode&unix.S_IFMT == unix.S_IFDIR
	if isDir != dir || (!isDir && st.Mode&unix.S_IFMT != unix.S_IFREG) || int(st.Uid) != me {
		return nil
	}
	if int(st.Gid) != gid {
		if err := unix.Fchown(fd, -1, gid); err != nil {
			return err
		}
	}
	perm := uint32(st.Mode) & 0o7777
	want := perm | 0o060
	if isDir {
		want |= 0o010 | unix.S_ISGID
	}
	if want != perm {
		if err := unix.Fchmod(fd, want); err != nil {
			return err
		}
	}
	return nil
}
