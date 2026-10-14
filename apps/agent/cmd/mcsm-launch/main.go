//go:build linux

// Command mcsm-launch starts a Minecraft server (or a server installer) as the
// dedicated server user, so that code running in the JVM — every mod and
// plugin — cannot read the panel's database, secrets, or other servers'
// launch tokens, which all belong to the agent's user.
//
//	mcsm-launch -- <program> [args...]
//
// It is installed root-owned, executable only by the agent's group, with the
// file capabilities cap_setuid and cap_setgid (see deploy/provision.sh). It
// can only ever switch to one user: the name in /etc/mcsm/server-user (a
// root-owned file) or, without it, "mcsm-mc". It refuses root, so holding the
// capability through this program never yields more than the server user.
//
// After switching it sets no_new_privs, clears its ambient capabilities and
// sets a group-writable umask (so the agent, a member of the server group, can
// manage what the server creates), then execs the program in place: the
// process keeps its pid, which is how the agent tracks and reattaches to it.
//
// Linux only.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	defaultUser = "mcsm-mc"
	userFile    = "/etc/mcsm/server-user"
	exitUsage   = 64
	exitRefused = 77
)

func main() {
	args := os.Args[1:]
	if len(args) < 2 || args[0] != "--" {
		fmt.Fprintln(os.Stderr, "usage: mcsm-launch -- <program> [args...]")
		os.Exit(exitUsage)
	}
	program, programArgs := args[1], args[1:]

	name, err := targetUser()
	if err != nil {
		refuse("%v", err)
	}
	u, err := user.Lookup(name)
	if err != nil {
		refuse("server user %q: %v", name, err)
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil {
		refuse("server user %q has a non-numeric id", name)
	}
	if uid == 0 || gid == 0 {
		refuse("refusing to run as root")
	}

	// Supplementary groups first (needs CAP_SETGID), then the group, then the
	// user, which drops the ability to change any of them again.
	if err := syscall.Setgroups([]int{gid}); err != nil {
		refuse("setgroups: %v", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		refuse("setgid: %v", err)
	}
	if err := syscall.Setuid(uid); err != nil {
		refuse("setuid: %v", err)
	}
	if os.Getuid() != uid || os.Geteuid() != uid || os.Getgid() != gid || os.Getegid() != gid {
		refuse("could not become %s", name)
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		refuse("no_new_privs: %v", err)
	}
	_ = unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0)
	syscall.Umask(0o002)

	path := program
	if !strings.Contains(program, "/") {
		if path, err = exec.LookPath(program); err != nil {
			refuse("%s: %v", program, err)
		}
	}
	env := withUser(os.Environ(), name)
	if err := syscall.Exec(path, programArgs, env); err != nil {
		refuse("exec %s: %v", path, err)
	}
}

// targetUser reads the configured server user. The file must belong to root
// and be writable by nobody else; otherwise the agent's user could point this
// program at any account it liked.
func targetUser() (string, error) {
	info, err := os.Stat(userFile)
	if os.IsNotExist(err) {
		return defaultUser, nil
	}
	if err != nil {
		return "", err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || info.Mode().Perm()&0o022 != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s must be a regular file owned by root and writable only by root", userFile)
	}
	b, err := os.ReadFile(userFile)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(b))
	if name == "" || name == "root" {
		return "", fmt.Errorf("%s names no usable user", userFile)
	}
	return name, nil
}

// withUser points HOME at the working directory (the server's own folder, the
// one place the server user can write) and names the user.
func withUser(env []string, name string) []string {
	cwd, _ := os.Getwd()
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "HOME", "USER", "LOGNAME":
			continue
		}
		out = append(out, kv)
	}
	if cwd != "" {
		out = append(out, "HOME="+cwd)
	}
	return append(out, "USER="+name, "LOGNAME="+name)
}

func refuse(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "mcsm-launch: "+format+"\n", a...)
	os.Exit(exitRefused)
}
