package isolation

import (
	"errors"
	"strings"
	"testing"
)

func TestParseMode(t *testing.T) {
	for v, want := range map[string]Mode{
		"": Auto, "auto": Auto, "1": Enforced, "true": Enforced, "enforce": Enforced,
		"0": Off, "off": Off, "FALSE": Off, "nonsense": Auto,
	} {
		if got := ParseMode(v); got != want {
			t.Errorf("%q -> %s, want %s", v, got, want)
		}
	}
}

func TestFromEnvDefaults(t *testing.T) {
	t.Setenv("AGENT_ISOLATE_SERVERS", "")
	t.Setenv("AGENT_SERVER_USER", "")
	t.Setenv("AGENT_LAUNCHER", "")
	cfg := FromEnv("/srv/servers")
	if cfg.Mode != Auto || cfg.User != DefaultUser || cfg.Launcher != DefaultLauncher {
		t.Fatalf("defaults: %+v", cfg)
	}
}

// Turned off, or unavailable in auto mode, a command runs unchanged: as the
// agent's own user, which is the opt-out's whole point.
func TestCommandUnchangedWhenNotIsolated(t *testing.T) {
	defer Configure(Config{Mode: Off})
	for _, mode := range []Mode{Off, Auto} {
		Configure(Config{Mode: mode, User: "no-such-user-for-tests", Launcher: "/no/such/launcher"})
		program, args, err := Command("java", []string{"-jar", "server.jar"}, t.TempDir())
		if err != nil || program != "java" || strings.Join(args, " ") != "-jar server.jar" {
			t.Fatalf("%s: %q %q %v", mode, program, args, err)
		}
		if Current().Active {
			t.Fatalf("%s: reported active without a launcher", mode)
		}
	}
}

// Enforced and unavailable refuses rather than quietly running a server as
// the agent's user.
func TestEnforcedRefusesWhenUnavailable(t *testing.T) {
	defer Configure(Config{Mode: Off})
	st := Configure(Config{Mode: Enforced, User: "no-such-user-for-tests", Launcher: "/no/such/launcher"})
	if st.Active || st.Reason == "" {
		t.Fatalf("status: %+v", st)
	}
	_, _, err := Command("java", nil, t.TempDir())
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "AGENT_ISOLATE_SERVERS=0") {
		t.Fatalf("err = %v", err)
	}
}
