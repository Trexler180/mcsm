//go:build linux

package isolation

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestIsolationEndToEnd runs only on a host set up like a provisioned one: the
// server user exists, the launcher is installed with its file capabilities,
// and this test runs as the agent's user holding CAP_KILL. deploy/isolation_e2e.sh
// builds exactly that in a container:
//
//	MCSM_ISOLATION_E2E=1 MCSM_SERVER_ROOT=/srv/servers go test ./internal/isolation -run EndToEnd
func TestIsolationEndToEnd(t *testing.T) {
	if os.Getenv("MCSM_ISOLATION_E2E") != "1" {
		t.Skip("set MCSM_ISOLATION_E2E=1 on a provisioned host (see deploy/isolation_e2e.sh)")
	}
	root := os.Getenv("MCSM_SERVER_ROOT")
	st := Configure(Config{Mode: Enforced, User: DefaultUser, Launcher: DefaultLauncher, ServerRoot: root})
	if !st.Active {
		t.Fatalf("isolation inactive: %s", st.Reason)
	}
	u, _ := user.Lookup(DefaultUser)
	serverUID, _ := strconv.Atoi(u.Uid)

	// A secret of the agent's, as the panel's database would be.
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, "smp")
	if err := os.MkdirAll(filepath.Join(dir, "world"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.properties"), []byte("motd=hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := `id -u; umask; grep NoNewPrivs /proc/self/status; grep CapEff /proc/self/status
echo edited >> server.properties
mkdir -p world/region && echo chunk > world/region/r.0.0.mca
cat ` + secret + ` 2>/dev/null && echo LEAKED || echo secret-unreadable`
	program, args, err := Command("sh", []string{"-c", script}, dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(program, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launch: %v: %s", err, out)
	}
	got := string(out)
	for _, want := range []string{strconv.Itoa(serverUID) + "\n", "0002\n", "NoNewPrivs:\t1", "CapEff:\t0000000000000000", "secret-unreadable"} {
		if !strings.Contains(got, want) {
			t.Errorf("server process output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "LEAKED") {
		t.Fatal("the server process read the agent's secret")
	}

	// The agent can still manage what the server wrote.
	if err := os.RemoveAll(filepath.Join(dir, "world")); err != nil {
		t.Fatalf("the agent could not delete the server's world: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "server.properties")); err != nil || !strings.Contains(string(b), "edited") {
		t.Fatalf("server.properties: %q %v", b, err)
	}

	// A long-running server is identified by its start time (its working
	// directory is hidden from the agent) and can be killed by the agent.
	program, args, _ = Command("sleep", []string{"60"}, dir)
	sleeper := exec.Command(program, args...)
	sleeper.Dir = dir
	sleeper.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	started := time.Now()
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := os.Readlink("/proc/" + strconv.Itoa(sleeper.Process.Pid) + "/cwd"); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("expected the server's cwd to be hidden, got %v", err)
	}
	if err := syscall.Kill(-sleeper.Process.Pid, syscall.SIGKILL); err != nil {
		t.Fatalf("the agent could not kill the server: %v", err)
	}
	_ = sleeper.Wait()
	_ = started
}
