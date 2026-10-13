package childenv

import (
	"strings"
	"testing"
)

func has(env []string, name string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, name+"=") {
			return true
		}
	}
	return false
}

func value(env []string, name string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
}

func TestBuildDropsAgentSecrets(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin", "HOME=/srv/mcsm", "LC_ALL=C.UTF-8",
		"AGENT_TOKEN=secret", "AGENT_TLS_KEY=/k", "JWT_SECRET=j", "LOCAL_AGENT_TOKEN=t",
		"SOMETHING_ELSE=x",
	}
	env := build(environ, "", nil)
	for _, name := range []string{"PATH", "HOME", "LC_ALL"} {
		if !has(env, name) {
			t.Errorf("expected %s to be passed through", name)
		}
	}
	for _, name := range []string{"AGENT_TOKEN", "AGENT_TLS_KEY", "JWT_SECRET", "LOCAL_AGENT_TOKEN", "SOMETHING_ELSE"} {
		if has(env, name) {
			t.Errorf("%s must not reach a child process", name)
		}
	}
}

func TestBuildPassthroughRefusesSecrets(t *testing.T) {
	environ := []string{"PATH=/usr/bin", "HTTPS_PROXY=http://proxy", "AGENT_TOKEN=secret", "JWT_SECRET=j"}
	env := build(environ, "HTTPS_PROXY, AGENT_TOKEN ,JWT_SECRET", nil)
	if !has(env, "HTTPS_PROXY") {
		t.Error("an operator passthrough should be honored")
	}
	if has(env, "AGENT_TOKEN") || has(env, "JWT_SECRET") {
		t.Error("a passthrough must never admit a secret")
	}
}

func TestBuildExtraWins(t *testing.T) {
	environ := []string{"PATH=/usr/bin", "TZ=UTC"}
	env := build(environ, "", []string{"TZ=Europe/Paris", "MCSM_TOKEN=link"})
	if got := value(env, "TZ"); got != "Europe/Paris" {
		t.Errorf("TZ = %q, want the explicit value", got)
	}
	if !has(env, "MCSM_TOKEN") {
		t.Error("explicitly injected link variables must survive")
	}
	count := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "TZ=") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("TZ appears %d times, want 1", count)
	}
}

func TestBuildNeverNil(t *testing.T) {
	if build(nil, "", nil) == nil {
		t.Fatal("Build must never return nil: a nil Env inherits the agent's environment")
	}
}

func TestBuildReadsTheRealEnvironment(t *testing.T) {
	t.Setenv("AGENT_TOKEN", "real-secret")
	t.Setenv(PassthroughVar, "")
	if has(Build(), "AGENT_TOKEN") {
		t.Fatal("AGENT_TOKEN leaked from the live environment")
	}
}
