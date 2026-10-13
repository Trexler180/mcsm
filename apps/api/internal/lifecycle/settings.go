package lifecycle

import "encoding/json"

// WithoutImportSettings drops the import marker from a server's settings once
// a reinstall has replaced the imported runtime with a managed one, keeping
// every unrelated key. Settings that cannot be parsed are returned unchanged.
func WithoutImportSettings(settings json.RawMessage) json.RawMessage {
	if len(settings) == 0 {
		return settings
	}
	m := map[string]any{}
	if err := json.Unmarshal(settings, &m); err != nil {
		return settings
	}
	if _, ok := m["import"]; !ok {
		return settings
	}
	delete(m, "import")
	out, err := json.Marshal(m)
	if err != nil {
		return settings
	}
	return out
}
