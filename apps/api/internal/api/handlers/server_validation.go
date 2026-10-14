package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/mcsm/api/internal/store"
)

// supportedPlatforms are the runtimes the agent knows how to install and run.
var supportedPlatforms = map[string]bool{
	"paper": true, "purpur": true, "vanilla": true, "fabric": true,
	"quilt": true, "spigot": true, "forge": true, "neoforge": true,
}

// versionRe is the shape of a Minecraft or loader version: "1.21.4",
// "24w14a", "1.20.1-pre1", "0.16.10", "47.2.0". Versions are interpolated into
// installer download URLs, so nothing outside this set is accepted.
var versionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$`)

const (
	maxServerNameLength = 64
	minRAMMb            = 256
	maxRAMMb            = 1 << 20 // 1 TiB: a sanity bound, not a recommendation
)

// fieldError names the input a validation failure is about.
type fieldError struct {
	Field string
	Msg   string
}

func (e *fieldError) Error() string { return e.Msg }

func badField(field, format string, args ...any) error {
	return &fieldError{Field: field, Msg: fmt.Sprintf(format, args...)}
}

// respondInvalid writes a validation failure: a field error when err names a
// field, else a plain 400.
func respondInvalid(w http.ResponseWriter, err error) {
	if fe, ok := asFieldError(err); ok {
		writeFieldError(w, fe.Field, fe.Msg)
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

// validateServerName trims and checks a server's display name.
func validateServerName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", badField("name", "name is required")
	}
	if len(name) > maxServerNameLength {
		return "", badField("name", "name must be at most %d characters", maxServerNameLength)
	}
	return name, nil
}

func validatePlatform(p string) error {
	if !supportedPlatforms[p] {
		return badField("platform", "platform must be one of paper, purpur, vanilla, fabric, quilt, spigot, forge or neoforge")
	}
	return nil
}

func validateVersion(field, v string) error {
	if !versionRe.MatchString(v) {
		return badField(field, "%s is not a valid version", field)
	}
	return nil
}

func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return badField("port", "port must be between 1 and 65535")
	}
	return nil
}

// validateRAM checks a server's memory bounds, against its node's memory when
// the node has reported it.
func validateRAM(minMb, maxMb int, node *store.Node) error {
	if minMb < minRAMMb {
		return badField("ram_mb_min", "minimum memory must be at least %d MB", minRAMMb)
	}
	if maxMb < minMb {
		return badField("ram_mb_max", "maximum memory must be at least the minimum")
	}
	if maxMb > maxRAMMb {
		return badField("ram_mb_max", "maximum memory is implausibly large")
	}
	if node != nil && node.MemoryMb != nil && *node.MemoryMb > 0 && maxMb > *node.MemoryMb {
		return badField("ram_mb_max", "maximum memory (%d MB) is more than the node has (%d MB)", maxMb, *node.MemoryMb)
	}
	return nil
}

// portTaken reports whether another server on the same node already uses port.
func portTaken(ctx context.Context, s *store.Store, nodeID, serverID string, port int) (string, error) {
	servers, err := s.ListServers(ctx)
	if err != nil {
		return "", err
	}
	for _, other := range servers {
		if other.NodeID == nodeID && other.ID != serverID && other.Port == port {
			return other.Name, nil
		}
	}
	return "", nil
}

// validateJarFile accepts only a bare .jar file name: the agent runs
// `java -jar <name>` in the server's own directory, so a path could reach a jar
// that belongs to another server.
func validateJarFile(name string) error {
	if name == "" {
		return nil
	}
	if name != path.Base(name) || strings.ContainsAny(name, `/\`) || name == ".." || strings.HasPrefix(name, "-") ||
		!strings.HasSuffix(strings.ToLower(name), ".jar") || len(name) > 255 {
		return badField("jar_file", "jar file must be a .jar file name in the server's own directory")
	}
	return nil
}

// resourcePackFields is the panel's record of a server's resource pack.
type resourcePackFields struct {
	Enabled   bool   `json:"enabled"`
	Path      string `json:"path"`
	PublicID  string `json:"public_id"`
	SHA1      string `json:"sha1"`
	FileName  string `json:"file_name"`
	Source    string `json:"source"`
	Required  bool   `json:"required"`
	Prompt    string `json:"prompt"`
	UpdatedAt string `json:"updated_at"`
}

// settingsKeys lists what may live in a server's settings object and whether a
// non-admin may change it. import decides which jar the agent runs and whether
// it installs a runtime, so only an administrator sets it.
var settingsKeys = map[string]bool{
	"helper_mod":      true,
	"resource_pack":   true,
	"connect_address": true,
	"import":          false,
}

// validateSettingsChange checks a new settings object against the current one.
// Keys a non-admin may not change must come back unchanged (the panel re-sends
// the whole object when it edits one key), unknown keys are refused, and the
// known ones are type-checked.
func validateSettingsChange(current, next json.RawMessage, isAdmin bool) (json.RawMessage, error) {
	if len(bytes.TrimSpace(next)) == 0 || bytes.Equal(bytes.TrimSpace(next), []byte("null")) {
		next = json.RawMessage("{}")
	}
	var nextMap map[string]json.RawMessage
	if err := json.Unmarshal(next, &nextMap); err != nil {
		return nil, badField("settings", "settings must be an object")
	}
	curMap := map[string]json.RawMessage{}
	if len(current) > 0 {
		_ = json.Unmarshal(current, &curMap)
	}

	for key, raw := range nextMap {
		editable, known := settingsKeys[key]
		unchanged := jsonEqual(curMap[key], raw)
		switch {
		case !known && !unchanged:
			return nil, badField("settings", "unknown setting %q", key)
		case !editable && !isAdmin && !unchanged:
			return nil, badField("settings", "only an administrator may change %q", key)
		}
		switch key {
		case "helper_mod":
			var b bool
			if err := json.Unmarshal(raw, &b); err != nil {
				return nil, badField("settings", "helper_mod must be true or false")
			}
		case "resource_pack":
			var rp resourcePackFields
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&rp); err != nil {
				return nil, badField("settings", "resource_pack is malformed")
			}
			if rp.Enabled && !isPublicResourcePackPath(rp.Path) {
				return nil, badField("settings", "the resource pack must be a .zip under /resource-packs/")
			}
			if len(rp.Prompt) > 1000 {
				return nil, badField("settings", "the resource pack prompt is too long")
			}
		case "connect_address":
			var addr string
			if err := json.Unmarshal(raw, &addr); err != nil || (addr != "" && !validConnectAddress(addr)) {
				return nil, badField("settings", "connect_address must be a host name or host:port")
			}
		case "import":
			var imp struct {
				JarFile   string `json:"jar_file"`
				NoInstall bool   `json:"no_install"`
			}
			if err := json.Unmarshal(raw, &imp); err != nil {
				return nil, badField("settings", "import is malformed")
			}
			if err := validateJarFile(imp.JarFile); err != nil {
				return nil, err
			}
		}
	}
	// A key a non-admin may not change must not be removed by omission either.
	if !isAdmin {
		for key, raw := range curMap {
			if editable, known := settingsKeys[key]; known && !editable {
				if _, present := nextMap[key]; !present {
					nextMap[key] = raw
				}
			}
		}
	}
	out, err := json.Marshal(nextMap)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func jsonEqual(a, b json.RawMessage) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return bytes.Equal(xa, ya)
}

// asFieldError unwraps a validation failure.
func asFieldError(err error) (*fieldError, bool) {
	var fe *fieldError
	if errors.As(err, &fe) {
		return fe, true
	}
	return nil, false
}

const (
	maxDescriptionLength = 500
	maxTags              = 20
	maxTagLength         = 32
)

func validateDescription(d *string) error {
	if d != nil && len(*d) > maxDescriptionLength {
		return badField("description", "description must be at most %d characters", maxDescriptionLength)
	}
	return nil
}

func validateTags(tags []string) error {
	if len(tags) > maxTags {
		return badField("tags", "at most %d tags", maxTags)
	}
	for _, t := range tags {
		if strings.TrimSpace(t) == "" || len(t) > maxTagLength {
			return badField("tags", "tags must be non-empty and at most %d characters", maxTagLength)
		}
	}
	return nil
}
