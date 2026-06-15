// Package spigotmc implements a SpigotMC plugin source backed by the Spiget
// API (api.spiget.org). It normalizes results into the modrinth package's wire
// shapes so the HTTP API and frontend can treat all sources uniformly — only
// the `source` field differs.
//
// Spiget's metadata is shallower than the other registries': versions carry no
// per-version game versions, changelogs, filenames, or hashes, and a
// resource's testedVersions list major lines only ("1.21") and is often stale.
// GetVersions therefore never filters by loader/MC version (the handler's
// version resolution would otherwise come up empty for perfectly working
// plugins) and downloads skip hash verification (no hash exists to check).
package spigotmc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mcsm/api/internal/mods/htmlmd"
	"github.com/mcsm/api/internal/mods/modrinth"
)

const (
	defaultBaseURL = "https://api.spiget.org/v2"
	defaultWebURL  = "https://www.spigotmc.org"
	userAgent      = "mcsm/1.0"
)

// errNotFound marks a Spiget 404, which on the search endpoints means "no
// results" rather than an error.
var errNotFound = fmt.Errorf("spiget returned 404")

type Client struct {
	http    *http.Client
	baseURL string
	webURL  string
}

func New() *Client {
	return &Client{
		http:    &http.Client{Timeout: 15 * time.Second},
		baseURL: defaultBaseURL,
		webURL:  defaultWebURL,
	}
}

// get performs a Spiget request; pageCount (when non-nil) receives the
// X-Page-Count header, Spiget's only total — it counts pages, not items.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any, pageCount *int) error {
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("spiget returned %d", resp.StatusCode)
	}
	if pageCount != nil {
		if n, err := strconv.Atoi(resp.Header.Get("X-Page-Count")); err == nil {
			*pageCount = n
		}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ── raw Spiget response shapes (subset) ──────────────────────────────

type spigetResource struct {
	ID             int      `json:"id"`
	Name           string   `json:"name"`
	Tag            string   `json:"tag"`
	TestedVersions []string `json:"testedVersions"`
	Icon           struct {
		URL string `json:"url"`
	} `json:"icon"`
	Premium  bool `json:"premium"`
	External bool `json:"external"`
	File     struct {
		Type string `json:"type"`
	} `json:"file"`
	Version struct {
		ID int `json:"id"`
	} `json:"version"`
	Likes          int    `json:"likes"`
	Downloads      int    `json:"downloads"`
	ReleaseDate    int64  `json:"releaseDate"`
	UpdateDate     int64  `json:"updateDate"`
	Description    string `json:"description"` // base64 HTML, full resource only
	SourceCodeLink string `json:"sourceCodeLink"`
}

type spigetVersion struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	ReleaseDate int64  `json:"releaseDate"`
}

// ── normalized API ───────────────────────────────────────────────────

// spigetSort maps the shared sort index to Spiget's sort parameter ("-" prefix
// = descending). Spiget has no relevance ranking (its search is a plain field
// match) and no follows; downloads and likes stand in.
func spigetSort(index string) string {
	switch index {
	case "follows":
		return "-likes"
	case "newest":
		return "-releaseDate"
	case "updated":
		return "-updateDate"
	default: // relevance, downloads
		return "-downloads"
	}
}

// Search queries Spiget. With a query it uses the name-field search endpoint
// (which cannot also filter by category); otherwise a numeric category filter
// browses that category, and plain browse lists free resources. MCVersion is
// ignored — testedVersions are major-line only and stale, so filtering on
// them hides working plugins.
func (c *Client) Search(ctx context.Context, p modrinth.SearchParams) (*modrinth.SearchResult, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}
	q := url.Values{
		"size": {fmt.Sprint(limit)},
		"page": {fmt.Sprint(p.Offset/limit + 1)},
		"sort": {spigetSort(p.Index)},
	}

	path := "/resources/free"
	switch {
	case p.Query != "":
		path = "/search/resources/" + url.PathEscape(p.Query)
		q.Set("field", "name")
	default:
		if id := firstNumeric(p.Categories); id != "" {
			path = "/categories/" + id + "/resources"
		}
	}

	var resources []spigetResource
	pages := 0
	if err := c.get(ctx, path, q, &resources, &pages); err != nil {
		// Spiget answers a search with no matches with 404.
		if err == errNotFound {
			return &modrinth.SearchResult{Hits: []modrinth.ProjectHit{}, Limit: limit}, nil
		}
		return nil, err
	}

	hits := make([]modrinth.ProjectHit, 0, len(resources))
	for _, r := range resources {
		hits = append(hits, c.resourceToHit(r))
	}
	total := pages * limit
	if total < p.Offset+len(hits) {
		total = p.Offset + len(hits)
	}
	return &modrinth.SearchResult{
		Hits:  hits,
		Total: total,
		Limit: limit,
	}, nil
}

func (c *Client) resourceToHit(r spigetResource) modrinth.ProjectHit {
	cats := []string{}
	if r.Premium {
		cats = append(cats, "premium")
	}
	return modrinth.ProjectHit{
		ProjectID:    fmt.Sprint(r.ID),
		Slug:         fmt.Sprint(r.ID),
		Title:        r.Name,
		Description:  r.Tag,
		Categories:   cats,
		ProjectType:  "plugin",
		Downloads:    r.Downloads,
		IconURL:      c.iconURL(r),
		Versions:     orVersions(r.TestedVersions),
		DateModified: unixToRFC3339(r.UpdateDate),
	}
}

func (c *Client) GetProject(ctx context.Context, projectID string) (*modrinth.Project, error) {
	var r spigetResource
	if err := c.get(ctx, "/resources/"+url.PathEscape(projectID), nil, &r, nil); err != nil {
		return nil, err
	}
	cats := []string{}
	if r.Premium {
		cats = append(cats, "premium")
	}
	p := &modrinth.Project{
		ID:          fmt.Sprint(r.ID),
		Slug:        fmt.Sprint(r.ID),
		Title:       r.Name,
		Description: r.Tag,
		Body:        decodeDescription(r.Description),
		Categories:  cats,
		Downloads:   r.Downloads,
		Followers:   r.Likes,
		IconURL:     c.iconURL(r),
		ProjectType: "plugin",
		Updated:     unixToRFC3339(r.UpdateDate),
	}
	if r.SourceCodeLink != "" {
		link := r.SourceCodeLink
		p.SourceURL = &link
	}
	return p, nil
}

// GetVersions lists a resource's update history, newest first. The loader and
// mcVersion arguments are intentionally ignored (see the package comment);
// every version reports the resource-level testedVersions and the full
// Bukkit-family loader list so plugin servers pass compatibility checks.
func (c *Client) GetVersions(ctx context.Context, projectID, loader, mcVersion string) ([]modrinth.Version, error) {
	r, raw, err := c.resourceAndVersions(ctx, projectID)
	if err != nil {
		return nil, err
	}
	versions := make([]modrinth.Version, 0, len(raw))
	for _, v := range raw {
		versions = append(versions, c.versionToModrinth(r, v))
	}
	return versions, nil
}

func (c *Client) GetVersion(ctx context.Context, projectID, versionID string) (*modrinth.Version, error) {
	var r spigetResource
	if err := c.get(ctx, "/resources/"+url.PathEscape(projectID), nil, &r, nil); err != nil {
		return nil, err
	}
	var v spigetVersion
	path := "/resources/" + url.PathEscape(projectID) + "/versions/" + url.PathEscape(versionID)
	if err := c.get(ctx, path, nil, &v, nil); err != nil {
		return nil, err
	}
	mv := c.versionToModrinth(&r, v)
	return &mv, nil
}

func (c *Client) resourceAndVersions(ctx context.Context, projectID string) (*spigetResource, []spigetVersion, error) {
	var r spigetResource
	if err := c.get(ctx, "/resources/"+url.PathEscape(projectID), nil, &r, nil); err != nil {
		return nil, nil, err
	}
	q := url.Values{"size": {"100"}, "sort": {"-releaseDate"}}
	var versions []spigetVersion
	if err := c.get(ctx, "/resources/"+url.PathEscape(projectID)+"/versions", q, &versions, nil); err != nil {
		return nil, nil, err
	}
	return &r, versions, nil
}

func (c *Client) versionToModrinth(r *spigetResource, v spigetVersion) modrinth.Version {
	f := modrinth.VersionFile{
		URL:      c.downloadURL(r, v.ID),
		Filename: jarFilename(r, v.Name),
		Primary:  true,
		// Spiget exposes no hashes; an empty SHA256 skips verification.
	}
	return modrinth.Version{
		ID:            fmt.Sprint(v.ID),
		ProjectID:     fmt.Sprint(r.ID),
		Name:          v.Name,
		VersionNumber: v.Name,
		VersionType:   "release",
		GameVersions:  orVersions(r.TestedVersions),
		Loaders:       []string{"paper", "purpur", "spigot", "bukkit"},
		Files:         []modrinth.VersionFile{f},
		DatePublished: unixToRFC3339(v.ReleaseDate),
	}
}

// downloadURL picks the Spiget endpoint that can actually serve the file. The
// latest version downloads through /resources/{id}/download, which also
// follows externally hosted files to their real location. Older versions only
// exist on Spiget's CDN proxy, which never mirrored external resources —
// those return "" and surface the standard "does not permit third-party
// downloads" error. Premium resources are never downloadable.
func (c *Client) downloadURL(r *spigetResource, versionID int) string {
	switch {
	case r.Premium:
		return ""
	case versionID == r.Version.ID:
		return fmt.Sprintf("%s/resources/%d/download", c.baseURL, r.ID)
	case r.External:
		return ""
	default:
		return fmt.Sprintf("%s/resources/%d/versions/%d/download/proxy", c.baseURL, r.ID, versionID)
	}
}

// GetCategories lists SpigotMC's resource categories. Spiget returns a flat
// list with duplicate names across parent groups; duplicates collapse onto the
// first id so the filter chips stay unique.
func (c *Client) GetCategories(ctx context.Context, projectType string) ([]modrinth.Category, error) {
	if projectType != "plugin" {
		return []modrinth.Category{}, nil
	}
	var raw []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	q := url.Values{"size": {"100"}}
	if err := c.get(ctx, "/categories", q, &raw, nil); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := make([]modrinth.Category, 0, len(raw))
	for _, cat := range raw {
		if cat.Name == "" || seen[cat.Name] {
			continue
		}
		seen[cat.Name] = true
		out = append(out, modrinth.Category{
			ID:          fmt.Sprint(cat.ID),
			Name:        cat.Name,
			ProjectType: "plugin",
			Header:      "categories",
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// decodeDescription turns Spiget's base64-encoded HTML resource description
// into Markdown for the detail dialog.
func decodeDescription(b64 string) string {
	if b64 == "" {
		return ""
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return ""
	}
	return htmlmd.ToMarkdown(string(data))
}

func (c *Client) iconURL(r spigetResource) string {
	if r.Icon.URL == "" {
		return ""
	}
	return c.webURL + "/" + strings.TrimPrefix(r.Icon.URL, "/")
}

// jarFilename builds a stable filename — Spiget has no per-version file
// metadata, so it is derived from the resource and version names.
func jarFilename(r *spigetResource, versionName string) string {
	ext := r.File.Type
	if !strings.HasPrefix(ext, ".") {
		ext = ".jar"
	}
	name := sanitizeFilename(r.Name)
	if name == "" {
		name = fmt.Sprint(r.ID)
	}
	if v := sanitizeFilename(versionName); v != "" {
		name += "-" + v
	}
	return name + ext
}

func sanitizeFilename(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		case r == ' ':
			return '-'
		default:
			return -1
		}
	}, s)
	return strings.Trim(s, "-.")
}

// orVersions never returns nil — the frontend calls .includes on it.
func orVersions(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func unixToRFC3339(ts int64) string {
	if ts == 0 {
		return ""
	}
	return time.Unix(ts, 0).UTC().Format(time.RFC3339)
}

func firstNumeric(list []string) string {
	for _, v := range list {
		if v == "" {
			continue
		}
		if _, err := strconv.Atoi(v); err == nil {
			return v
		}
	}
	return ""
}
