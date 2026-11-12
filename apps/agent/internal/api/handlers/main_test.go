package handlers

import (
	"os"
	"testing"

	"github.com/shirou/gopsutil/v4/disk"
)

// The handlers refuse backups and world imports that would leave the disk
// nearly full. Tests report a roomy disk so they pass on a machine that is
// short of space; the guard itself is tested through backupBlockedMsg and
// unpackBlockedMsg.
func TestMain(m *testing.M) {
	diskUsage = func(string) (*disk.UsageStat, error) {
		return &disk.UsageStat{Total: 1000 * uint64(gib), Free: 900 * uint64(gib)}, nil
	}
	os.Exit(m.Run())
}
