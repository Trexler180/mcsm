// Package hangar implements a PaperMC Hangar plugin source. It normalizes
// results into the modrinth package's wire shapes so the HTTP API and frontend
// can treat all sources uniformly — only the `source` field differs.
//
// Hangar (hangar.papermc.io) hosts Paper-family plugins only, so every request
// pins platform=PAPER and every result is a "plugin" project. The numeric
// project id is used as the canonical ProjectID (the API accepts it wherever a
// slug is, and version pluginDependencies reference dependencies by numeric
// id, which makes recursive installs dedupe correctly). Slug carries
// "owner/slug" because Hangar web URLs need both parts.
package hangar

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mcsm/api/internal/mods/modrinth"
)

const (
	defaultBaseURL = "https://hangar.papermc.io/api/v1"
	userAgent      = "mcsm/1.0"
)

type Client struct {
	http    *http.Client
	baseURL string
}

func New() *Client {
	return &Client{
		http:    &http.Client{Timeout: 15 * time.Second},
		baseURL: defaultBaseURL,
	}
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
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
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hangar returned %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ── raw Hangar response shapes (subset) ──────────────────────────────

type hangarProject struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Namespace struct {
		Owner string `json:"owner"`
		Slug  string `json:"slug"`
	} `json:"namespace"`
	Description string `json:"description"`
	Category    string `json:"category"`
	LastUpdated string `json:"lastUpdated"`
	AvatarURL   string `json:"avatarUrl"`
	Stats       struct {
		Downloads int `json:"downloads"`
		Stars     int `json:"stars"`
	} `json:"stats"`
	SupportedPlatforms map[string][]string `json:"supportedPlatforms"`
	MainPageContent    string              `json:"mainPageContent"`
	Settings           struct {
		Links []struct {
			Links []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"links"`
		} `json:"links"`
	} `json:"settings"`
}

type hangarVersion struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"createdAt"`
	Channel     struct {
		Name string `json:"name"`
	} `json:"channel"`
	Downloads map[string]struct {
		FileInfo struct {
			Name       string `json:"name"`
			SizeBytes  int    `json:"sizeBytes"`
			SHA256Hash string `json:"sha256Hash"`
		} `json:"fileInfo"`
		ExternalURL string `json:"externalUrl"`
		DownloadURL string `json:"downloadUrl"`
	} `json:"downloads"`
	PluginDependencies map[string][]struct {
		Name      string `json:"name"`
		ProjectID int    `json:"projectId"`
		Required  bool   `json:"required"`
	} `json:"pluginDependencies"`
	PlatformDependencies map[string][]string `json:"platformDependencies"`
}

// ── normalized API ───────────────────────────────────────────────────

// hangarSort maps the shared sort index to Hangar's sort parameter ("-" prefix
// = descending). Hangar has no follows; stars are the closest equivalent.
// Relevance is Hangar's implicit query ranking, so it sends no sort when a
// query is present and falls back to downloads when browsing.
func hangarSort(index, query string) string {
	switch index {
	case "downloads":
		return "-downloads"
	case "follows":
		return "-stars"
	case "newest":
		return "-newest"
	case "updated":
		return "-updated"
	default: // relevance
		if query != "" {
			return ""
		}
		return "-downloads"
	}
}

func (c *Client) Search(ctx context.Context, p modrinth.SearchParams) (*modrinth.SearchResult, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}
	q := url.Values{
		"limit":    {fmt.Sprint(limit)},
		"offset":   {fmt.Sprint(p.Offset)},
		"platform": {"PAPER"},
	}
	if p.Query != "" {
		q.Set("q", p.Query)
	}
	if s := hangarSort(p.Index, p.Query); s != "" {
		q.Set("sort", s)
	}
	for _, cat := range p.Categories {
		if cat != "" {
			q.Add("category", cat)
		}
	}
	// The version filter requires the platform parameter, which is always set.
	if p.MCVersion != "" {
		q.Set("version", p.MCVersion)
	}

	var raw struct {
		Pagination struct {
			Count int `json:"count"`
		} `json:"pagination"`
		Result []hangarProject `json:"result"`
	}
	if err := c.get(ctx, "/projects", q, &raw); err != nil {
		return nil, err
	}

	hits := make([]modrinth.ProjectHit, 0, len(raw.Result))
	for _, m := range raw.Result {
		hits = append(hits, modrinth.ProjectHit{
			ProjectID:    fmt.Sprint(m.ID),
			Slug:         m.Namespace.Owner + "/" + m.Namespace.Slug,
			Title:        m.Name,
			Author:       m.Namespace.Owner,
			Description:  m.Description,
			Categories:   categoryList(m.Category),
			ProjectType:  "plugin",
			Downloads:    m.Stats.Downloads,
			IconURL:      m.AvatarURL,
			Versions:     m.SupportedPlatforms["PAPER"],
			DateModified: m.LastUpdated,
		})
	}
	return &modrinth.SearchResult{
		Hits:  hits,
		Total: raw.Pagination.Count,
		Limit: limit,
	}, nil
}

func (c *Client) GetProject(ctx context.Context, projectID string) (*modrinth.Project, error) {
	var m hangarProject
	if err := c.get(ctx, "/projects/"+url.PathEscape(projectID), nil, &m); err != nil {
		return nil, err
	}
	p := &modrinth.Project{
		ID:          fmt.Sprint(m.ID),
		Slug:        m.Namespace.Owner + "/" + m.Namespace.Slug,
		Title:       m.Name,
		Description: m.Description,
		Body:        m.MainPageContent,
		Categories:  categoryList(m.Category),
		Downloads:   m.Stats.Downloads,
		Followers:   m.Stats.Stars,
		IconURL:     m.AvatarURL,
		ProjectType: "plugin",
		Updated:     m.LastUpdated,
	}
	// Hangar links are free-form named groups; match the common names onto the
	// dedicated fields the detail dialog renders.
	for _, group := range m.Settings.Links {
		for _, l := range group.Links {
			if l.URL == "" {
				continue
			}
			name := strings.ToLower(l.Name)
			u := l.URL
			switch {
			case p.SourceURL == nil && strings.Contains(name, "source"):
				p.SourceURL = &u
			case p.IssuesURL == nil && strings.Contains(name, "issue"):
				p.IssuesURL = &u
			case p.WikiURL == nil && (strings.Contains(name, "wiki") || strings.Contains(name, "doc")):
				p.WikiURL = &u
			}
		}
	}
	return p, nil
}

// GetVersions lists a project's Paper versions, newest first (Hangar's
// ordering). The loader argument is ignored — everything on Hangar reached
// through platform=PAPER runs on the Paper family. mcVersion filters
// client-side against the version's Paper platform dependencies.
func (c *Client) GetVersions(ctx context.Context, projectID, loader, mcVersion string) ([]modrinth.Version, error) {
	q := url.Values{"limit": {"50"}, "platform": {"PAPER"}}
	var raw struct {
		Result []hangarVersion `json:"result"`
	}
	if err := c.get(ctx, "/projects/"+url.PathEscape(projectID)+"/versions", q, &raw); err != nil {
		return nil, err
	}
	versions := make([]modrinth.Version, 0, len(raw.Result))
	for _, v := range raw.Result {
		if _, ok := v.Downloads["PAPER"]; !ok {
			continue
		}
		if mcVersion != "" && !matchesMC(v.PlatformDependencies["PAPER"], mcVersion) {
			continue
		}
		versions = append(versions, versionToModrinth(projectID, v))
	}
	return versions, nil
}

func (c *Client) GetVersion(ctx context.Context, projectID, versionID string) (*modrinth.Version, error) {
	var v hangarVersion
	path := "/projects/" + url.PathEscape(projectID) + "/versions/" + url.PathEscape(versionID)
	if err := c.get(ctx, path, nil, &v); err != nil {
		return nil, err
	}
	mv := versionToModrinth(projectID, v)
	return &mv, nil
}

// GetCategories returns Hangar's fixed category enum (the API has no tag
// endpoint). ID carries the enum value Search filters by.
func (c *Client) GetCategories(ctx context.Context, projectType string) ([]modrinth.Category, error) {
	if projectType != "plugin" {
		return []modrinth.Category{}, nil
	}
	enum := []struct{ id, name string }{
		{"admin_tools", "Admin Tools"},
		{"chat", "Chat"},
		{"dev_tools", "Dev Tools"},
		{"economy", "Economy"},
		{"gameplay", "Gameplay"},
		{"games", "Games"},
		{"protection", "Protection"},
		{"role_playing", "Role Playing"},
		{"world_management", "World Management"},
		{"misc", "Misc"},
	}
	out := make([]modrinth.Category, 0, len(enum))
	for _, e := range enum {
		out = append(out, modrinth.Category{
			ID:          e.id,
			Name:        e.name,
			ProjectType: "plugin",
			Header:      "categories",
		})
	}
	return out, nil
}

func versionToModrinth(projectID string, v hangarVersion) modrinth.Version {
	out := modrinth.Version{
		ID:            fmt.Sprint(v.ID),
		ProjectID:     projectID,
		Name:          v.Name,
		VersionNumber: v.Name,
		VersionType:   channelToType(v.Channel.Name),
		Changelog:     v.Description,
		GameVersions:  v.PlatformDependencies["PAPER"],
		Loaders:       []string{"paper", "purpur", "folia"},
		DatePublished: v.CreatedAt,
	}
	if d, ok := v.Downloads["PAPER"]; ok {
		f := modrinth.VersionFile{
			URL:      d.DownloadURL,
			Filename: d.FileInfo.Name,
			Primary:  true,
			Size:     d.FileInfo.SizeBytes,
		}
		// Externally hosted files have no Hangar download URL (and no hash to
		// verify); fetch them from where the author points.
		if f.URL == "" {
			f.URL = d.ExternalURL
		} else {
			f.Hashes.SHA256 = d.FileInfo.SHA256Hash
		}
		if f.Filename == "" {
			f.Filename = sanitizeFilename(v.Name) + ".jar"
		}
		out.Files = []modrinth.VersionFile{f}
	}
	// Plugin dependencies referencing other Hangar projects carry numeric ids
	// the install recursion can resolve; externally hosted dependencies have
	// projectId 0 and are skipped (matching how empty ProjectIDs are handled).
	for _, dep := range v.PluginDependencies["PAPER"] {
		if dep.ProjectID == 0 {
			continue
		}
		depType := "optional"
		if dep.Required {
			depType = "required"
		}
		out.Dependencies = append(out.Dependencies, modrinth.Dependency{
			ProjectID:      fmt.Sprint(dep.ProjectID),
			DependencyType: depType,
		})
	}
	return out
}

// channelToType maps Hangar release channels onto Modrinth version types so
// the existing channel badges render.
func channelToType(channel string) string {
	switch strings.ToLower(channel) {
	case "beta":
		return "beta"
	case "alpha", "snapshot":
		return "alpha"
	default:
		return "release"
	}
}

// matchesMC reports whether a version's Paper platform dependencies cover
// mcVersion. Authors sometimes declare only the major line ("1.21"), so a
// major-line entry also matches its patch releases ("1.21.4").
func matchesMC(supported []string, mcVersion string) bool {
	for _, v := range supported {
		if v == mcVersion || strings.HasPrefix(mcVersion, v+".") {
			return true
		}
	}
	return false
}

func categoryList(category string) []string {
	if category == "" {
		return []string{}
	}
	return []string{strings.ReplaceAll(category, "_", " ")}
}

func sanitizeFilename(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, s)
	return strings.Trim(s, "-")
}
