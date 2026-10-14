package process

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// The directory registry maps server ids to their directories. It used to live
// only in memory, so after an agent restart every file, player and backup
// request failed until the panel re-sent the mapping, and the panel re-sent it
// before nearly every request to be safe. It is now persisted beside the run
// state and reloaded at startup.

func registryPath(serverRoot string) string {
	return filepath.Join(runtimeRoot(serverRoot), "registry.json")
}

// loadRegistry reads the persisted registry, keeping only directories inside
// the server root: the file sits on disk where a misbehaving process could
// edit it, and an entry pointing elsewhere must not become a file-manager root.
func loadRegistry(serverRoot string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(registryPath(serverRoot))
	if err != nil {
		return out
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		log.Printf("directory registry unreadable, ignoring it: %v", err)
		return out
	}
	for id, dir := range raw {
		if withinRoot(serverRoot, dir) {
			out[id] = dir
		} else {
			log.Printf("directory registry: dropping %s, %q is outside the server root", id, dir)
		}
	}
	return out
}

// saveRegistry writes the registry atomically. A failure is logged and
// otherwise ignored: the panel can always register a directory again.
func saveRegistry(serverRoot string, dirs map[string]string) {
	if err := ensureRuntimeRoot(serverRoot); err != nil {
		log.Printf("directory registry: %v", err)
		return
	}
	data, err := json.MarshalIndent(dirs, "", "  ")
	if err != nil {
		return
	}
	tmp := registryPath(serverRoot) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		log.Printf("directory registry: %v", err)
		return
	}
	if err := os.Rename(tmp, registryPath(serverRoot)); err != nil {
		log.Printf("directory registry: %v", err)
	}
}

func withinRoot(root, dir string) bool {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(absRoot), filepath.Clean(absDir))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
