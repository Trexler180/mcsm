package modrinth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const baseURL = "https://api.modrinth.com/v2"

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

func (c *Client) Search(ctx context.Context, query, loader, mcVersion string, limit int) (*SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	facets := `[["project_type:mod"],["server_side:optional","server_side:required"]]`
	if loader != "" {
		facets = fmt.Sprintf(`[["project_type:mod"],["categories:%s"],["server_side:optional","server_side:required"]]`, loader)
	}

	params := url.Values{
		"query":  {query},
		"facets": {facets},
		"limit":  {fmt.Sprint(limit)},
	}
	if mcVersion != "" {
		params.Set("facets", fmt.Sprintf(`[["project_type:mod"],["versions:%s"],["server_side:optional","server_side:required"]]`, mcVersion))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/search?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mcsm/0.1.0 (github.com/mcsm)")

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
	req.Header.Set("User-Agent", "mcsm/0.1.0")

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
	req.Header.Set("User-Agent", "mcsm/0.1.0")

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

func (c *Client) GetVersion(ctx context.Context, versionID string) (*Version, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/version/"+versionID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mcsm/0.1.0")

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
