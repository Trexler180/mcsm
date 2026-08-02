package process

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// findWhitelist returns the whitelist.json entry with the given name
// (case-insensitive), or nil when absent.
func findWhitelist(wl []whitelistEntry, name string) *whitelistEntry {
	for i := range wl {
		if strings.EqualFold(wl[i].Name, name) {
			return &wl[i]
		}
	}
	return nil
}

// bedrockManager wires a manager to a server with Floodgate installed and a
// stub gamertag service that knows one player.
func bedrockManager(t *testing.T, config string) (*Manager, string, string) {
	t.Helper()
	stubGeyserAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/xbox/xuid/Cool Guy" {
			w.Write([]byte(`{"xuid":2535453759792258}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	dir := floodgateServer(t, config)
	m := NewManager(t.TempDir())
	const id = "srv1"
	m.RegisterDir(id, dir)
	return m, id, dir
}

const coolGuyUUID = "00000000-0000-0000-0009-01fb54b26482"

func TestBedrockWhitelistAddWritesFloodgateIdentity(t *testing.T) {
	m, id, dir := bedrockManager(t, "username-prefix: .\n")

	err := m.ApplyPlayerAction(id, PlayerAction{
		Action:   "whitelist_add",
		Bedrock:  true,
		Gamertag: "Cool Guy",
	})
	if err != nil {
		t.Fatalf("whitelist_add: %v", err)
	}

	wl := readWhitelist(dir)
	e := findWhitelist(wl, ".Cool_Guy")
	if e == nil {
		t.Fatalf("no entry written, got %#v", wl)
	}
	// The UUID is what vanilla actually matches on. A name-only entry is the
	// defect this path exists to prevent.
	if e.UUID != coolGuyUUID {
		t.Errorf("uuid = %q, want %q", e.UUID, coolGuyUUID)
	}
	// The roster must read this back as a Bedrock player, not a Java one.
	if !isBedrockUUID(e.UUID) {
		t.Error("written uuid should be recognised as Bedrock")
	}
}

func TestBedrockWhitelistAddIgnoresClientSuppliedUUID(t *testing.T) {
	m, id, dir := bedrockManager(t, "username-prefix: .\n")

	// A gamertag is authoritative: whatever UUID and name the caller also sent
	// are discarded, so a stale preview cannot whitelist the wrong person.
	err := m.ApplyPlayerAction(id, PlayerAction{
		Action:   "whitelist_add",
		Bedrock:  true,
		Gamertag: "Cool Guy",
		UUID:     "00000000-0000-0000-0009-0000deadbeef",
		Name:     ".Someone_Else",
	})
	if err != nil {
		t.Fatalf("whitelist_add: %v", err)
	}

	wl := readWhitelist(dir)
	if len(wl) != 1 {
		t.Fatalf("want exactly one entry, got %#v", wl)
	}
	if wl[0].UUID != coolGuyUUID || wl[0].Name != ".Cool_Guy" {
		t.Errorf("entry = %#v, want the resolved identity", wl[0])
	}
}

func TestBedrockWhitelistAddUsesKnownUUIDWithoutGamertag(t *testing.T) {
	m, id, dir := bedrockManager(t, "username-prefix: .\n")

	// The roster row menu acts on a player whose Floodgate UUID the server
	// already knows, so no lookup is needed — and none should be attempted,
	// since a truncated stored name cannot be reversed into a gamertag.
	err := m.ApplyPlayerAction(id, PlayerAction{
		Action:  "whitelist_add",
		Bedrock: true,
		UUID:    coolGuyUUID,
		Name:    ".Cool_Guy",
	})
	if err != nil {
		t.Fatalf("whitelist_add: %v", err)
	}
	if e := findWhitelist(readWhitelist(dir), ".Cool_Guy"); e == nil || e.UUID != coolGuyUUID {
		t.Fatalf("entry = %#v", e)
	}
}

func TestBedrockWhitelistAddRefusesWithoutIdentity(t *testing.T) {
	m, id, dir := bedrockManager(t, "username-prefix: .\n")

	// Neither a gamertag nor a Floodgate UUID: writing a name-only entry here
	// would look successful in the UI and admit nobody, so it must fail loudly.
	err := m.ApplyPlayerAction(id, PlayerAction{
		Action:  "whitelist_add",
		Bedrock: true,
		Name:    ".Cool_Guy",
	})
	if err == nil {
		t.Fatal("want an error when no Floodgate identity is available")
	}
	if len(readWhitelist(dir)) != 0 {
		t.Errorf("nothing should have been written, got %#v", readWhitelist(dir))
	}
}

func TestBedrockWhitelistAddWritesNothingOnFailedLookup(t *testing.T) {
	m, id, dir := bedrockManager(t, "username-prefix: .\n")

	err := m.ApplyPlayerAction(id, PlayerAction{
		Action:   "whitelist_add",
		Bedrock:  true,
		Gamertag: "Nobody",
	})
	if !errors.Is(err, ErrGamertagNotFound) {
		t.Fatalf("err = %v, want ErrGamertagNotFound", err)
	}
	if len(readWhitelist(dir)) != 0 {
		t.Errorf("a failed lookup must write nothing, got %#v", readWhitelist(dir))
	}
}

func TestBedrockWhitelistAddReplacesStaleName(t *testing.T) {
	m, id, dir := bedrockManager(t, "username-prefix: .\n")

	// The same player under an older gamertag. XUIDs survive a rename, so this
	// entry has the right UUID and the wrong name.
	writeFile(t, filepath.Join(dir, "whitelist.json"),
		`[{"uuid":"`+coolGuyUUID+`","name":".Old_Name"}]`)

	err := m.ApplyPlayerAction(id, PlayerAction{
		Action:   "whitelist_add",
		Bedrock:  true,
		Gamertag: "Cool Guy",
	})
	if err != nil {
		t.Fatalf("whitelist_add: %v", err)
	}

	wl := readWhitelist(dir)
	if len(wl) != 1 {
		t.Fatalf("re-adding should refresh the entry, not duplicate it: %#v", wl)
	}
	if wl[0].Name != ".Cool_Guy" {
		t.Errorf("name = %q, want the current .Cool_Guy", wl[0].Name)
	}
}

func TestBedrockWhitelistRemoveByUUID(t *testing.T) {
	m, id, dir := bedrockManager(t, "username-prefix: .\n")

	// A renamed player: the stored name no longer matches what the panel shows,
	// so only a UUID match can remove them.
	writeFile(t, filepath.Join(dir, "whitelist.json"),
		`[{"uuid":"`+coolGuyUUID+`","name":".Old_Name"},`+
			`{"uuid":"11111111-2222-3333-4444-555555555555","name":"JavaFriend"}]`)

	err := m.ApplyPlayerAction(id, PlayerAction{
		Action:  "whitelist_remove",
		Bedrock: true,
		UUID:    coolGuyUUID,
		Name:    ".Cool_Guy",
	})
	if err != nil {
		t.Fatalf("whitelist_remove: %v", err)
	}

	wl := readWhitelist(dir)
	if len(wl) != 1 || wl[0].Name != "JavaFriend" {
		t.Fatalf("want only the Java player left, got %#v", wl)
	}
}

func TestBedrockWhitelistRemoveFallsBackToName(t *testing.T) {
	m, id, dir := bedrockManager(t, "username-prefix: .\n")

	// An entry whose UUID the caller doesn't have to hand still has to be
	// removable, or the UI offers an action that silently does nothing.
	writeFile(t, filepath.Join(dir, "whitelist.json"),
		`[{"uuid":"","name":".Cool_Guy"}]`)

	err := m.ApplyPlayerAction(id, PlayerAction{
		Action:  "whitelist_remove",
		Bedrock: true,
		Name:    ".Cool_Guy",
	})
	if err != nil {
		t.Fatalf("whitelist_remove: %v", err)
	}
	if wl := readWhitelist(dir); len(wl) != 0 {
		t.Fatalf("entry should be gone, got %#v", wl)
	}
}

func TestBedrockWhitelistRemoveNeedsATarget(t *testing.T) {
	m, id, _ := bedrockManager(t, "username-prefix: .\n")

	err := m.ApplyPlayerAction(id, PlayerAction{Action: "whitelist_remove", Bedrock: true})
	if err == nil {
		t.Fatal("want an error when neither uuid nor name is given")
	}
}

func TestBedrockWhitelistKeepsSpacesWhenSubstitutionDisabled(t *testing.T) {
	m, id, dir := bedrockManager(t, "username-prefix: .\nreplace-spaces: false\n")

	// With substitution off the Java-side name genuinely contains a space. The
	// entry only ever reaches a JSON file, never the console, so it must be
	// written rather than rejected as an unsafe name.
	err := m.ApplyPlayerAction(id, PlayerAction{
		Action:   "whitelist_add",
		Bedrock:  true,
		Gamertag: "Cool Guy",
	})
	if err != nil {
		t.Fatalf("whitelist_add: %v", err)
	}
	if e := findWhitelist(readWhitelist(dir), ".Cool Guy"); e == nil {
		t.Fatalf("want a space-bearing entry, got %#v", readWhitelist(dir))
	}
}

func TestJavaWhitelistPathUnchanged(t *testing.T) {
	m, id, dir := bedrockManager(t, "username-prefix: .\n")

	// Without the Bedrock flag an offline whitelist add keeps taking the
	// original ops/whitelist file path, resolving the UUID the old way.
	writeFile(t, filepath.Join(dir, "server.properties"), "online-mode=false\n")
	err := m.ApplyPlayerAction(id, PlayerAction{Action: "whitelist_add", Name: "JavaFriend"})
	if err != nil {
		t.Fatalf("whitelist_add: %v", err)
	}

	e := findWhitelist(readWhitelist(dir), "JavaFriend")
	if e == nil {
		t.Fatalf("no entry written, got %#v", readWhitelist(dir))
	}
	// Offline mode yields the deterministic offline UUID, not a Floodgate one.
	if e.UUID != offlineUUID("JavaFriend") {
		t.Errorf("uuid = %q, want the offline-mode uuid %q", e.UUID, offlineUUID("JavaFriend"))
	}
	if isBedrockUUID(e.UUID) {
		t.Error("a Java player should not get a Bedrock uuid")
	}
}
