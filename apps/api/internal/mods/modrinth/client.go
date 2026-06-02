package modrinth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	baseURL   = "https://api.modrinth.com/v2"
	userAgent = "mcsm/0.1.0 (github.com/mcsm)"
)

type Client struct {
	http *http.Client
}

func New() *Client {
	return &Client{
		http: &http.Client{Timeout: 15 * time.Second},
	}
}

type SearchResult struct {
	Hits  []ProjectHit `json:"hits"`
	Total int          `json:"total_hits"`
	Limit int          `json:"limit"`
}

type ProjectHit struct {
	ProjectID    string   `json:"project_id"`
	Slug         string   `json:"slug"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Categories   []string `json:"categories"`
	ClientSide   string   `json:"client_side"`
	ServerSide   string   `json:"server_side"`
	ProjectType  string   `json:"project_type"`
	Downloads    int      `json:"downloads"`
	IconURL      string   `json:"icon_url"`
	Versions     []string `json:"versions"`
	DateModified string   `json:"date_modified"`
}

type Project struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Categories  []string `json:"categories"`
	ClientSide  string   `json:"client_side"`
	ServerSide  string   `json:"server_side"`
	Downloads   int      `json:"downloads"`
	IconURL     string   `json:"icon_url"`
}

type Version struct {
	ID            string        `json:"id"`
	ProjectID     string        `json:"project_id"`
	Name          string        `json:"name"`
	VersionNumber string        `json:"version_number"`
	GameVersions  []string      `json:"game_versions"`
	Loaders       []string      `json:"loaders"`
	Files         []VersionFile `json:"files"`
	Dependencies  []Dependency  `json:"dependencies"`
	DatePublished string        `json:"date_published"`
}

type VersionFile struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Primary  bool   `json:"primary"`
	Size     int    `json:"size"`
	Hashes   struct {
		SHA256 string `json:"sha256"`
	} `json:"hashes"`
}

type Dependency struct {
	ProjectID      string `json:"project_id"`
	VersionID      string `json:"version_id"`
	DependencyType string `json:"dependency_type"`
}

// SearchParams describes a Modrinth search. Zero values are omitted.
type SearchParams struct {
	Query       string
	ProjectType string   // mod, plugin, datapack, modpack, shader, resourcepack
	Loader      string   // fabric, forge, paper, ...
	MCVersion   string   // e.g. 1.21.4
	Categories  []string // extra category facets (and-ed)
	Index       string   // relevance|downloads|follows|newest|updated
	Limit       int
	Offset      int
}

// buildFacets composes Modrinth's nested facet array. Each inner slice is OR-ed,
// the outer slices are AND-ed. Previously version/loader/type clobbered each
// other (A1) — now every constraint is appended so they all apply together.
func (p SearchParams) buildFacets() string {
	pt := p.ProjectType
	if pt == "" {
		pt = "mod"
	}
	groups := [][]string{{"project_type:" + pt}}

	if p.Loader != "" {
		groups = append(groups, []string{"categories:" + p.Loader})
	}
	for _, cat := range p.Categories {
		if cat != "" {
			groups = append(groups, []string{"categories:" + cat})
		}
	}
	if p.MCVersion != "" {
		groups = append(groups, []string{"versions:" + p.MCVersion})
	}
	// Server-relevant content only: keep client-only resources out of mod/plugin
	// searches but allow resource/shader packs (which are client_side:required).
	if pt == "mod" || pt == "plugin" || pt == "datapack" || pt == "modpack" {
		groups = append(groups, []string{"server_side:optional", "server_side:required"})
	}

	parts := make([]string, len(groups))
	for i, g := range groups {
		quoted := make([]string, len(g))
		for j, v := range g {
			quoted[j] = fmt.Sprintf("%q", v)
		}
		parts[i] = "[" + strings.Join(quoted, ",") + "]"
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func (c *Client) Search(ctx context.Context, p SearchParams) (*SearchResult, error) {
	if p.Limit <= 0 {
		p.Limit = 20
	}

	params := url.Values{
		"query":  {p.Query},
		"facets": {p.buildFacets()},
		"limit":  {fmt.Sprint(p.Limit)},
	}
	if p.Offset > 0 {
		params.Set("offset", fmt.Sprint(p.Offset))
	}
	if p.Index != "" {
		params.Set("index", p.Index)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("modrinth returned %d", resp.StatusCode)
	}

	var result SearchResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) GetVersions(ctx context.Context, projectID, loader, mcVersion string) ([]Version, error) {
	params := url.Values{}
	if loader != "" {
		params.Set("loaders", fmt.Sprintf(`["%s"]`, loader))
	}
	if mcVersion != "" {
		params.Set("game_versions", fmt.Sprintf(`["%s"]`, mcVersion))
	}

	reqURL := fmt.Sprintf("%s/project/%s/version", baseURL, projectID)
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("modrinth returned %d", resp.StatusCode)
	}

	var versions []Version
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		return nil, err
	}
	return versions, nil
}

func (c *Client) GetProject(ctx context.Context, projectID string) (*Project, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/project/"+projectID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("modrinth returned %d", resp.StatusCode)
	}

	var p Project
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return nil, err
	}
	return &p, nil
}

// LoaderForPlatform maps a server platform to the Modrinth loader facet used for
// version compatibility filtering. Bukkit-family servers run plugins under the
// "paper"/"spigot"/"bukkit"/"purpur" loaders; modloaders map 1:1. Vanilla has no
// loader (datapacks only) and returns "".
func LoaderForPlatform(platform string) string {
	switch strings.ToLower(platform) {
	case "fabric":
		return "fabric"
	case "quilt":
		return "quilt"
	case "forge":
		return "forge"
	case "neoforge":
		return "neoforge"
	case "paper":
		return "paper"
	case "purpur":
		return "purpur"
	case "spigot":
		return "spigot"
	case "bukkit":
		return "bukkit"
	default:
		return ""
	}
}

// IsPluginPlatform reports whether the platform loads Bukkit-style plugins
// (target dir /plugins) rather than mods (/mods).
func IsPluginPlatform(platform string) bool {
	switch strings.ToLower(platform) {
	case "paper", "purpur", "spigot", "bukkit":
		return true
	default:
		return false
	}
}

// Download streams a file to a temp file on disk, verifying its SHA256 against
// wantSHA (when non-empty). Returns the temp file path; caller must remove it.
// Streaming + temp-file avoids holding multi-MB jars in memory (A5) and lets us
// reject a corrupt/MITM download before it ever reaches the agent (A4).
func (c *Client) Download(ctx context.Context, fileURL, wantSHA string) (path string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download returned %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "mcsm-mod-*.jar")
	if err != nil {
		return "", err
	}
	defer func() {
		tmp.Close()
		if err != nil {
			os.Remove(tmp.Name())
		}
	}()

	h := sha256.New()
	if _, err = io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		return "", err
	}

	if wantSHA != "" {
		got := hex.EncodeToString(h.Sum(nil))
		if !strings.EqualFold(got, wantSHA) {
			err = fmt.Errorf("sha256 mismatch: want %s got %s", wantSHA, got)
			return "", err
		}
	}
	return tmp.Name(), nil
}

func (c *Client) GetVersion(ctx context.Context, versionID string) (*Version, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/version/"+versionID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("modrinth returned %d", resp.StatusCode)
	}

	var v Version
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	return &v, nil
}
