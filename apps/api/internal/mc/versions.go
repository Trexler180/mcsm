// Package mc fetches available Minecraft game versions and mod-loader versions
// from upstream metadata services (Mojang, FabricMC, QuiltMC), with a short
// in-memory cache so the panel can offer version dropdowns instead of free text.
package mc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	mojangManifest = "https://launchermeta.mojang.com/mc/game/version_manifest_v2.json"
	fabricGame     = "https://meta.fabricmc.net/v2/versions/game"
	fabricLoader   = "https://meta.fabricmc.net/v2/versions/loader"
	quiltGame      = "https://meta.quiltmc.org/v3/versions/game"
	quiltLoader    = "https://meta.quiltmc.org/v3/versions/loader"
)

type GameVersion struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

type Client struct {
	http *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry
	ttl   time.Duration
}

type cacheEntry struct {
	data    any
	expires time.Time
}

func New() *Client {
	return &Client{
		http:  &http.Client{Timeout: 15 * time.Second},
		cache: map[string]cacheEntry{},
		ttl:   time.Hour,
	}
}

func (c *Client) cached(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.data, true
}

func (c *Client) store(key string, data any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = cacheEntry{data: data, expires: time.Now().Add(c.ttl)}
}

func (c *Client) getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "mcsm/0.1.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// GameVersions returns the Minecraft versions available for a platform, newest
// first. Fabric/Quilt report their own supported lists; everything else uses the
// Mojang manifest. When includeSnapshots is false, only stable releases.
func (c *Client) GameVersions(ctx context.Context, platform string, includeSnapshots bool) ([]GameVersion, error) {
	platform = strings.ToLower(platform)
	key := fmt.Sprintf("game:%s:%t", platform, includeSnapshots)
	if v, ok := c.cached(key); ok {
		return v.([]GameVersion), nil
	}

	var out []GameVersion
	var err error
	switch platform {
	case "fabric":
		out, err = c.fabricLikeGame(ctx, fabricGame, includeSnapshots)
	case "quilt":
		out, err = c.fabricLikeGame(ctx, quiltGame, includeSnapshots)
	default:
		out, err = c.mojangGame(ctx, includeSnapshots)
	}
	if err != nil {
		return nil, err
	}
	c.store(key, out)
	return out, nil
}

func (c *Client) fabricLikeGame(ctx context.Context, url string, includeSnapshots bool) ([]GameVersion, error) {
	var raw []GameVersion
	if err := c.getJSON(ctx, url, &raw); err != nil {
		return nil, err
	}
	out := make([]GameVersion, 0, len(raw))
	for _, v := range raw {
		if !includeSnapshots && !v.Stable {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

func (c *Client) mojangGame(ctx context.Context, includeSnapshots bool) ([]GameVersion, error) {
	var manifest struct {
		Versions []struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"versions"`
	}
	if err := c.getJSON(ctx, mojangManifest, &manifest); err != nil {
		return nil, err
	}
	out := make([]GameVersion, 0, len(manifest.Versions))
	for _, v := range manifest.Versions {
		stable := v.Type == "release"
		if !includeSnapshots && !stable {
			continue
		}
		out = append(out, GameVersion{Version: v.ID, Stable: stable})
	}
	return out, nil
}

// LoaderVersions returns mod-loader versions for fabric/quilt, newest first.
// Other platforms have no separate loader list and return an empty slice.
func (c *Client) LoaderVersions(ctx context.Context, platform string) ([]GameVersion, error) {
	platform = strings.ToLower(platform)
	key := "loader:" + platform
	if v, ok := c.cached(key); ok {
		return v.([]GameVersion), nil
	}

	var url string
	switch platform {
	case "fabric":
		url = fabricLoader
	case "quilt":
		url = quiltLoader
	default:
		return []GameVersion{}, nil
	}

	var raw []struct {
		Version string `json:"version"`
		Stable  bool   `json:"stable"`
	}
	if err := c.getJSON(ctx, url, &raw); err != nil {
		return nil, err
	}
	out := make([]GameVersion, 0, len(raw))
	for _, v := range raw {
		out = append(out, GameVersion{Version: v.Version, Stable: v.Stable})
	}
	c.store(key, out)
	return out, nil
}
