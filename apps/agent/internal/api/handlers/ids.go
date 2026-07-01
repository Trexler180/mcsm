package handlers

import (
	"fmt"
	"regexp"
)

// pathIDRe matches the identifiers the panel actually generates (UUIDs and
// "backup-<unix>" names). The first character must be alphanumeric, which
// rules out ".."/dotfiles, and no path separators are accepted, so a matching
// ID can never escape the directory it is joined into.
var pathIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// validatePathID rejects identifiers that are unsafe to use as a filesystem
// path segment. Every handler that joins a caller-supplied server or backup ID
// into a path must call this first.
func validatePathID(s string) error {
	if !pathIDRe.MatchString(s) {
		return fmt.Errorf("invalid id %q", s)
	}
	return nil
}
