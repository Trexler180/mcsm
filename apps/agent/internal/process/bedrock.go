package process

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Whitelisting a Bedrock player is not the same operation as whitelisting a Java
// one. A running server resolves `whitelist add <name>` against Mojang, which has
// no record of a Floodgate name, and vanilla's whitelist is keyed on the profile
// UUID — so an entry lacking the player's real Floodgate UUID admits nobody. To
// whitelist someone who has never joined (the only case whitelisting really
// matters for) their identity has to be minted ahead of time: gamertag -> Xbox
// XUID -> the UUID Floodgate itself would derive from it.

var (
	// ErrGamertagNotFound means the lookup succeeded and no such Xbox account
	// exists. Retrying will not help; the admin mistyped the gamertag.
	ErrGamertagNotFound = errors.New("no Xbox account has that gamertag")

	// ErrBedrockLookupUnavailable means the lookup itself failed — network error,
	// timeout, or rate limit. The gamertag may be perfectly valid, so this is
	// reported separately: the admin should retry rather than second-guess it.
	ErrBedrockLookupUnavailable = errors.New("could not reach the GeyserMC gamertag service")
)

// geyserAPIBase is GeyserMC's public gamertag service. It is a variable so tests
// can point the resolver at a local stub instead of the internet.
var geyserAPIBase = "https://api.geysermc.org"

// maxJavaNameLen is Minecraft's username length limit. Floodgate truncates
// prefix+gamertag to fit it, so a whitelist entry has to be truncated the same
// way to match what the player actually connects as.
const maxJavaNameLen = 16

// bedrockLookupTTL is how long a resolved gamertag is reused. Gamertag→XUID
// mappings are effectively static, so this is about call volume rather than
// freshness: the panel previews as the admin types, and without memoisation each
// keystroke would reach a third-party API from a single shared IP.
const bedrockLookupTTL = 10 * time.Minute

// validGamertagRe matches alphanumeric words separated by single spaces, which
// is the Xbox gamertag character set. Anchoring the spaces this way also rejects
// leading, trailing and doubled spaces without a second pass.
var validGamertagRe = regexp.MustCompile(`^[A-Za-z0-9]+( [A-Za-z0-9]+)*$`)

// BedrockIdentity is the Java-side identity Floodgate gives a Bedrock player on
// a particular server — everything needed to write a whitelist entry that the
// player will actually match when they connect.
type BedrockIdentity struct {
	Gamertag string `json:"gamertag"`

	// XUID is carried as a decimal string deliberately: it is a 64-bit value and
	// JSON numbers are doubles, so a browser would silently round a large one.
	XUID string `json:"xuid"`

	UUID string `json:"uuid"`

	// Name is what lands in whitelist.json and shows in-game: the Floodgate
	// prefix plus the gamertag, spaces substituted and length-truncated exactly
	// as Floodgate does it.
	Name string `json:"name"`
}

// validGamertag reports whether s could be an Xbox gamertag. Microsoft's current
// rule is stricter still (3–12 characters, must start with a letter) but legacy
// gamertags predate it and remain in use, so this only rejects what is certainly
// not a gamertag rather than enforcing the modern signup rules.
func validGamertag(s string) bool {
	return s != "" && len([]rune(s)) <= maxJavaNameLen && validGamertagRe.MatchString(s)
}

// floodgateUUID reproduces Floodgate's `new UUID(0, xuid)`: the high 64 bits are
// zero and the low 64 hold the XUID, giving the 00000000-0000-0000-XXXX-…
// signature that isBedrockUUID recognises when reading a roster back.
func floodgateUUID(xuid uint64) string {
	return fmt.Sprintf("00000000-0000-0000-%04x-%012x", xuid>>48, xuid&0xffff_ffff_ffff)
}

// floodgateName derives the Java-side username Floodgate assigns to a gamertag
// on a server with the given config.
func floodgateName(gamertag string, cfg floodgateCfg) string {
	if cfg.ReplaceSpaces {
		gamertag = strings.ReplaceAll(gamertag, " ", "_")
	}
	name := cfg.Prefix + gamertag
	if r := []rune(name); len(r) > maxJavaNameLen {
		name = string(r[:maxJavaNameLen])
	}
	return name
}

// gamertagFromInput recovers the gamertag from whatever the admin typed. They
// may enter the gamertag itself ("Cool Guy") or the Floodgate name they can see
// in-game (".Cool_Guy"), so the prefix is stripped when present and underscores
// are mapped back to spaces on servers that substitute them. That reverse
// mapping is unambiguous: Xbox gamertags contain no underscores, so any
// underscore in a Floodgate name was a space.
func gamertagFromInput(input string, cfg floodgateCfg) string {
	tag := strings.TrimSpace(input)
	if cfg.Prefix != "" {
		tag = strings.TrimPrefix(tag, cfg.Prefix)
	}
	if cfg.ReplaceSpaces {
		tag = strings.ReplaceAll(tag, "_", " ")
	}
	return strings.TrimSpace(tag)
}

// ResolveBedrock turns whatever the admin typed into the Java-side identity
// Floodgate would give that player on this server. Read-only: it changes
// nothing, so the panel can call it freely to preview a whitelist entry before
// committing to it.
func (m *Manager) ResolveBedrock(id, input string) (BedrockIdentity, error) {
	dir, ok := m.GetDir(id)
	if !ok {
		return BedrockIdentity{}, fmt.Errorf("server directory not registered")
	}
	cfg, _ := floodgateConfig(dir)

	gamertag := gamertagFromInput(input, cfg)
	if !validGamertag(gamertag) {
		return BedrockIdentity{}, fmt.Errorf("gamertags are up to 16 letters, digits and single spaces")
	}

	xuid, err := m.lookupXUID(gamertag)
	if err != nil {
		return BedrockIdentity{}, err
	}
	return BedrockIdentity{
		Gamertag: gamertag,
		XUID:     strconv.FormatUint(xuid, 10),
		UUID:     floodgateUUID(xuid),
		Name:     floodgateName(gamertag, cfg),
	}, nil
}

// bedrockLookup is one memoised gamertag resolution. err is stored alongside the
// result so a known-bad gamertag is remembered too — an admin backspacing
// through a typo would otherwise re-query the API on every keystroke.
type bedrockLookup struct {
	xuid uint64
	err  error
	at   time.Time
}

// lookupXUID resolves a gamertag to its Xbox XUID, memoising the outcome.
func (m *Manager) lookupXUID(gamertag string) (uint64, error) {
	key := strings.ToLower(gamertag)

	m.bedrockMu.Lock()
	c, cached := m.bedrockCache[key]
	m.bedrockMu.Unlock()
	if cached && time.Since(c.at) < bedrockLookupTTL {
		return c.xuid, c.err
	}

	xuid, err := fetchXUID(gamertag)

	// A transient outage is not a fact about the gamertag, so it is never cached:
	// the admin will retry within seconds and should get a real answer, not a
	// ten-minute-old failure.
	if !errors.Is(err, ErrBedrockLookupUnavailable) {
		m.bedrockMu.Lock()
		m.bedrockCache[key] = bedrockLookup{xuid: xuid, err: err, at: time.Now()}
		m.bedrockMu.Unlock()
	}
	return xuid, err
}

// fetchXUID asks GeyserMC's public gamertag service for an XUID. Every failure
// is mapped onto one of the two sentinel errors so callers — and ultimately the
// admin — can tell "no such player" apart from "couldn't check".
func fetchXUID(gamertag string) (uint64, error) {
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Get(geyserAPIBase + "/v2/xbox/xuid/" + url.PathEscape(gamertag))
	if err != nil {
		return 0, ErrBedrockLookupUnavailable
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusBadRequest:
		return 0, ErrGamertagNotFound
	default:
		return 0, ErrBedrockLookupUnavailable
	}

	var body struct {
		XUID uint64 `json:"xuid"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body) != nil {
		return 0, ErrBedrockLookupUnavailable
	}
	// The service answers 200 with a message body for some unknown gamertags, so
	// a missing or zero XUID means "not found" rather than a malformed reply.
	if body.XUID == 0 {
		return 0, ErrGamertagNotFound
	}
	return body.XUID, nil
}
