//go:build linux

package hardening

import "golang.org/x/sys/unix"

func apply() (bool, error) {
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return false, err
	}
	return true, nil
}
