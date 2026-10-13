//go:build linux

package hardening

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestApplyMakesProcessNonDumpable(t *testing.T) {
	applied, err := Apply()
	if err != nil || !applied {
		t.Fatalf("Apply() = %v, %v", applied, err)
	}
	dumpable, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if dumpable != 0 {
		t.Fatalf("dumpable = %d, want 0", dumpable)
	}
}
