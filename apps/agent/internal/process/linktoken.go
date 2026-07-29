package process

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
)

// Launch tokens authenticate the helper mod's WebSocket link back to the agent.
//
// A token is minted per launch, injected into the server process's environment,
// and persisted alongside the rest of the run state. It is never written into
// the server's own directory — which matters because that directory is copied by
// the clone and backup features, and a long-lived secret there would leak into
// every backup.
//
// Persistence exists for exactly one reason: an agent restart must not orphan a
// server that kept running. On reattach the process still holds the token it was
// given at spawn, so the agent has to remember it to accept the reconnect.

// launchTokenBytes is the entropy per token. 32 bytes is well beyond what a
// loopback-only, short-lived credential needs, and costs nothing.
const launchTokenBytes = 32

// LinkAgentURL is the base URL a spawned server should dial back on, e.g.
// "http://127.0.0.1:8090". Set once at agent startup from the address the agent
// actually bound.
//
// Empty disables the helper link entirely: no token is minted, no environment is
// injected, and every server behaves exactly as it did before the mod existed.
// That is the correct default for anything that constructs a Manager without a
// running HTTP listener, including tests.
var LinkAgentURL string

// newLaunchToken mints a fresh token.
func newLaunchToken() (string, error) {
	buf := make([]byte, launchTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ValidateLaunchToken reports whether token matches the one issued to serverID's
// current launch. It satisfies link.TokenValidator.
//
// The comparison is constant-time. The endpoint is loopback-only, but a timing
// oracle on a credential is the kind of thing that stops being theoretical the
// moment someone runs the agent somewhere unexpected.
func (m *Manager) ValidateLaunchToken(serverID, token string) bool {
	if serverID == "" || token == "" {
		return false
	}

	st, err := readRunState(m.serverRoot, serverID)
	if err != nil || st == nil || st.LinkToken == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(st.LinkToken), []byte(token)) == 1
}

// linkEnviron returns the environment entries that tell the helper mod how to
// reach this agent. Returned as KEY=VALUE pairs ready to append to a command's
// environment.
//
// An empty result means the mod stays dormant, which is the correct behaviour
// whenever we cannot establish a trustworthy link.
func linkEnviron(agentURL, serverID, token string) []string {
	if agentURL == "" || serverID == "" || token == "" {
		return nil
	}
	return []string{
		"MCSM_AGENT_URL=" + agentURL,
		"MCSM_SERVER_ID=" + serverID,
		"MCSM_TOKEN=" + token,
	}
}
