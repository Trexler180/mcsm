// Package curseforge implements a CurseForge (Eternal API) mod source. It
// normalizes results into the modrinth package's wire shapes so the HTTP API and
// frontend can treat both sources uniformly — only the `source` field differs.
//
// Requires CURSEFORGE_API_KEY. When unset, New returns a client whose Enabled()
// is false and whose calls return ErrDisabled.
package curseforge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mcsm/api/internal/mods/modrinth"
)

const (
	baseURL       = "https://api.curseforge.com"
	minecraftGame = 432
)

// ErrDisabled is returned when no API key is configured.
var ErrDisabled = fmt.Errorf("curseforge source disabled: set CURSEFORGE_API_KEY")

type Client struct {
	http   *http.Client
	apiKey string
}

func New() *Client {
	return &Client{
		http:   &http.Client{Timeout: 15 * time.Second},
		apiKey: os.Getenv("CURSEFORGE_API_KEY"),
	}
}

func (c *Client) Enabled() bool { return c.apiKey != "" }

// classID maps a project type to the CurseForge class taxonomy.
func classID(projectType string) int {
	switch projectType {
	case "plugin":
		return 5
	case "resourcepack":
		return 12
	case "modpack":
		return 4471
	case "shader":
		return 6552
	case "datapack":
		return 6945
	default: // mod
		return 6
	}
}

// modLoaderType maps a loader name to the CurseForge numeric enum (0 = any).
func modLoaderType(loader string) int {
	switch strings.ToLower(loader) {
	case "forge":
		return 1
	case "fabric":
		return 4
	case "quilt":
		return 5
	case "neoforge":
		return 6
	default:
		return 0
	}
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	u := baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("curseforge returned %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ── raw CF response shapes (subset) ──────────────────────────────────

type cfMod struct {
	ID            int    `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	Summary       string `json:"summary"`
	DownloadCount int    `json:"downloadCount"`
	Logo          struct {
		URL string `json:"url"`
	} `json:"logo"`
	Categories []struct {
		Name string `json:"name"`
	} `json:"categories"`
	LatestFiles []cfFile `json:"latestFiles"`
}

type cfFile struct {
	ID           int      `json:"id"`
	DisplayName  string   `json:"displayName"`
	FileName     string   `json:"fileName"`
	DownloadURL  string   `json:"downloadUrl"`
	GameVersions []string `json:"gameVersions"`
	Hashes       []struct {
		Value string `json:"value"`
		Algo  int    `json:"algo"`
	} `json:"hashes"`
}

// ── normalized API ───────────────────────────────────────────────────

func (c *Client) Search(ctx context.Context, p modrinth.SearchParams) (*modrinth.SearchResult, error) {
	q := url.Values{
		"gameId":    {fmt.Sprint(minecraftGame)},
		"classId":   {fmt.Sprint(classID(p.ProjectType))},
		"pageSize":  {fmt.Sprint(orDefault(p.Limit, 20))},
		"index":     {fmt.Sprint(p.Offset)},
		"sortField": {"2"}, // popularity
		"sortOrder": {"desc"},
	}
	if p.Query != "" {
		q.Set("searchFilter", p.Query)
	}
	if p.MCVersion != "" {
		q.Set("gameVersion", p.MCVersion)
	}
	if ml := modLoaderType(p.Loader); ml != 0 {
		q.Set("modLoaderType", fmt.Sprint(ml))
	}

	var raw struct {
		Data       []cfMod `json:"data"`
		Pagination struct {
			TotalCount int `json:"totalCount"`
		} `json:"pagination"`
	}
	if err := c.get(ctx, "/v1/mods/search", q, &raw); err != nil {
		return nil, err
	}

	hits := make([]modrinth.ProjectHit, 0, len(raw.Data))
	for _, m := range raw.Data {
		hits = append(hits, modrinth.ProjectHit{
			ProjectID:   fmt.Sprint(m.ID),
			Slug:        m.Slug,
			Title:       m.Name,
			Description: m.Summary,
			Categories:  categoryNames(m),
			Downloads:   m.DownloadCount,
			IconURL:     m.Logo.URL,
			ProjectType: orString(p.ProjectType, "mod"),
		})
	}
	return &modrinth.SearchResult{
		Hits:  hits,
		Total: raw.Pagination.TotalCount,
		Limit: orDefault(p.Limit, 20),
	}, nil
}

func (c *Client) GetProject(ctx context.Context, projectID string) (*modrinth.Project, error) {
	var raw struct {
		Data cfMod `json:"data"`
	}
	if err := c.get(ctx, "/v1/mods/"+projectID, nil, &raw); err != nil {
		return nil, err
	}
	m := raw.Data
	return &modrinth.Project{
		ID:          fmt.Sprint(m.ID),
		Slug:        m.Slug,
		Title:       m.Name,
		Description: m.Summary,
		Categories:  categoryNames(m),
		Downloads:   m.DownloadCount,
		IconURL:     m.Logo.URL,
	}, nil
}

func (c *Client) GetVersions(ctx context.Context, projectID, loader, mcVersion string) ([]modrinth.Version, error) {
	q := url.Values{}
	if mcVersion != "" {
		q.Set("gameVersion", mcVersion)
	}
	if ml := modLoaderType(loader); ml != 0 {
		q.Set("modLoaderType", fmt.Sprint(ml))
	}
	var raw struct {
		Data []cfFile `json:"data"`
	}
	if err := c.get(ctx, "/v1/mods/"+projectID+"/files", q, &raw); err != nil {
		return nil, err
	}
	versions := make([]modrinth.Version, 0, len(raw.Data))
	for _, f := range raw.Data {
		versions = append(versions, fileToVersion(projectID, loader, f))
	}
	return versions, nil
}

func (c *Client) GetVersion(ctx context.Context, projectID, fileID string) (*modrinth.Version, error) {
	var raw struct {
		Data cfFile `json:"data"`
	}
	if err := c.get(ctx, "/v1/mods/"+projectID+"/files/"+fileID, nil, &raw); err != nil {
		return nil, err
	}
	v := fileToVersion(projectID, "", raw.Data)
	return &v, nil
}

func fileToVersion(projectID, loader string, f cfFile) modrinth.Version {
	loaders := []string{}
	if loader != "" {
		loaders = []string{loader}
	}
	vf := modrinth.VersionFile{
		URL:      f.DownloadURL,
		Filename: f.FileName,
		Primary:  true,
	}
	// CF hashes are sha1(1)/md5(2); we don't have sha256, so leave it blank
	// (Download skips verification when empty).
	return modrinth.Version{
		ID:            fmt.Sprint(f.ID),
		ProjectID:     projectID,
		Name:          f.DisplayName,
		VersionNumber: f.FileName,
		GameVersions:  f.GameVersions,
		Loaders:       loaders,
		Files:         []modrinth.VersionFile{vf},
	}
}

func categoryNames(m cfMod) []string {
	out := make([]string, 0, len(m.Categories))
	for _, c := range m.Categories {
		out = append(out, c.Name)
	}
	return out
}

func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

func orString(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
