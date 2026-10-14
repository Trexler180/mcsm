//go:build linux

package hardening

import "golang.org/x/sys/unix"

func apply() (bool, error) {
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return false, err
	}
	// The agent may hold CAP_KILL (to stop servers running as another user)
	// as an ambient capability. Clearing the ambient set keeps it for the
	// agent but stops every child — a server run without isolation, an
	// installer — from inheriting it.
	_ = unix.Prctl(unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0, 0, 0)
	return true, nil
}
