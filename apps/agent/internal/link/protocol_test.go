package link

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fixturesDir is the shared contract directory. It is deliberately reached by
// relative path rather than copied: one edit to a fixture must be able to break
// both language's tests at once, which is impossible if each side owns a copy.
const fixturesDir = "../../../mod/fixtures"

// TestFixturesRoundTrip is the guard described in PROTOCOL.md §6. For every
// canonical frame it parses into the native types, re-serialises, and requires
// semantic equality with the original bytes.
//
// A field present on the wire but missing from the Go structs is silently
// dropped by encoding/json, so it would vanish on re-serialisation and fail
// here. That is exactly the drift this test exists to catch.
func TestFixturesRoundTrip(t *testing.T) {
	entries, err := os.ReadDir(fixturesDir)
	if err != nil {
		t.Fatalf("read fixtures dir: %v", err)
	}

	var seen int
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		seen++

		t.Run(entry.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(fixturesDir, entry.Name()))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			var env Envelope
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("unmarshal envelope: %v", err)
			}
			if env.V != ProtocolVersion {
				t.Fatalf("fixture declares protocol version %d, want %d", env.V, ProtocolVersion)
			}

			payload, err := decodePayload(env)
			if err != nil {
				t.Fatalf("decode payload: %v", err)
			}

			data, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal payload: %v", err)
			}
			env.Data = data

			got, err := json.Marshal(env)
			if err != nil {
				t.Fatalf("marshal envelope: %v", err)
			}

			assertSemanticallyEqual(t, raw, got)
		})
	}

	// A fixtures directory that quietly emptied would make this whole test pass
	// while checking nothing.
	if seen == 0 {
		t.Fatal("no fixtures found — the shared contract is missing")
	}
}

// decodePayload converts a frame's data into its concrete type.
func decodePayload(env Envelope) (any, error) {
	switch env.Type {
	case TypeHello:
		var v Hello
		return &v, json.Unmarshal(env.Data, &v)
	case TypeWelcome:
		var v Welcome
		return &v, json.Unmarshal(env.Data, &v)
	case TypeSnapshot:
		var v Snapshot
		return &v, json.Unmarshal(env.Data, &v)
	case TypeEvent:
		var v Event
		return &v, json.Unmarshal(env.Data, &v)
	case TypeRPCRequest:
		var v RPCRequest
		return &v, json.Unmarshal(env.Data, &v)
	case TypeRPCResponse:
		var v RPCResponse
		return &v, json.Unmarshal(env.Data, &v)
	default:
		return nil, errUnknownType{env.Type}
	}
}

type errUnknownType struct{ typ string }

func (e errUnknownType) Error() string {
	return "fixture uses unknown frame type: " + e.typ
}

// assertSemanticallyEqual compares two JSON documents ignoring key order and
// insignificant formatting, but not ignoring missing or extra keys.
func assertSemanticallyEqual(t *testing.T, want, got []byte) {
	t.Helper()

	var wantAny, gotAny any
	if err := json.Unmarshal(want, &wantAny); err != nil {
		t.Fatalf("unmarshal expected: %v", err)
	}
	if err := json.Unmarshal(got, &gotAny); err != nil {
		t.Fatalf("unmarshal actual: %v", err)
	}

	if !reflect.DeepEqual(wantAny, gotAny) {
		wantPretty, _ := json.MarshalIndent(wantAny, "", "  ")
		gotPretty, _ := json.MarshalIndent(gotAny, "", "  ")
		t.Errorf("round-trip changed the frame.\n--- fixture ---\n%s\n--- after round-trip ---\n%s",
			wantPretty, gotPretty)
	}
}

// TestSnapshotEmptyCollectionsSurviveRoundTrip pins the boundary case that
// naive implementations get wrong: an empty list must stay [] and must not
// become null, because the agent treats the snapshot player list as truth. A
// null list read as "unknown" rather than "nobody online" would leave stale
// players online forever in player_sessions.
func TestSnapshotEmptyCollectionsSurviveRoundTrip(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(fixturesDir, "snapshot_empty_server.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	var snap Snapshot
	if err := json.Unmarshal(env.Data, &snap); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	if snap.Players.List == nil {
		t.Error("players.list decoded to nil; an empty server must yield an empty slice, not nil")
	}
	if snap.Dimensions == nil {
		t.Error("dimensions decoded to nil; expected an empty slice")
	}

	out, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var check map[string]json.RawMessage
	if err := json.Unmarshal(out, &check); err != nil {
		t.Fatalf("unmarshal re-encoded: %v", err)
	}
	if string(check["dimensions"]) != "[]" {
		t.Errorf("dimensions re-encoded as %s, want []", check["dimensions"])
	}
}

// TestHelloCapabilities covers the capability negotiation the agent relies on to
// stay compatible with adapters that cannot do everything the Fabric build can.
func TestHelloCapabilities(t *testing.T) {
	h := Hello{Capabilities: []string{CapVitals, CapRPC}}

	if !h.HasCapability(CapVitals) {
		t.Error("expected vitals capability to be reported")
	}
	if h.HasCapability(CapCommandExec) {
		t.Error("command_exec was not advertised but was reported as present")
	}

	var empty Hello
	if empty.HasCapability(CapVitals) {
		t.Error("a mod advertising nothing must not report capabilities")
	}
}
