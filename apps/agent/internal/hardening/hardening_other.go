//go:build !linux

package hardening

func apply() (bool, error) { return false, nil }
