// Package curseforge implements a CurseForge (Eternal API) mod source. It
// normalizes results into the modrinth package's wire shapes so the HTTP API and
// frontend can treat both sources uniformly — only the `source` field differs.
//
// Search and project metadata use the documented Core API and require an API
// key, supplied via the key provider passed to New (the app-secret store, with
// CURSEFORGE_API_KEY as an env fallback) and managed in Settings → Integrations.
// CurseForge gates search behind a key plus Cloudflare, so there is no reliable
// keyless search path — the UI shows an "add a key" prompt until one is set.
// For self-hosted Core API mirrors, CURSEFORGE_SEARCH_PROXY accepts a
// comma-separated list tried in order (a backup mirror covers the primary going
// down). All HTTP calls retry transient failures (network errors, 429, 5xx)
// with backoff and a per-attempt timeout, so a momentary blip no longer fails
// the request. File listings, single files, and downloads use the
// anonymous curseforge.com website API (the same endpoints the website itself
// calls) when key-less, so installs and update checks never depend on the
// proxy — only discovery does. The website download redirect is additionally
// used as a fallback when the Core API returns no downloadUrl (mod authors
// can disable third-party API downloads).
package curseforge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mcsm/api/internal/mods/modrinth"
)

const (
	defaultCoreURL = "https://api.curseforge.com"
	defaultWebURL  = "https://www.curseforge.com"
	minecraftGame  = 432
	userAgent      = "mcsm/1.0"

	// maxAttempts bounds the retries per base URL; perAttemptTimeout caps each
	// individual request so a hung connection is abandoned with budget left to
	// retry or fail over within the caller's deadline.
	maxAttempts       = 3
	perAttemptTimeout = 6 * time.Second
	retryBaseDelay    = 300 * time.Millisecond

	// keyCacheTTL bounds how long currentKey reuses a keyFn result before
	// re-reading it, so a key pasted in Settings takes effect within seconds
	// without hitting the store on every request.
	keyCacheTTL = 30 * time.Second
)

// ErrDisabled is returned by Search, GetCategories, and GetProject when no API
// key is configured and no search proxy is set. Version listing and downloads
// never require a key.
var ErrDisabled = fmt.Errorf("curseforge search requires an API key — add one in Settings → Integrations")

type Client struct {
	http       *http.Client
	keyFn      func() string
	coreURL    string
	webURL     string
	proxyURLs  []string
	retryDelay time.Duration

	mu        sync.Mutex
	cachedKey string
	keyExpiry time.Time
}

// New builds a client whose API key is resolved lazily via keyFn. keyFn is
// called at most once per keyCacheTTL (it may read the app-secret store, so we
// don't want it on every request) and may return "" when no key is configured.
// Pass nil in tests that set fields directly.
func New(keyFn func() string) *Client {
	proxyURLs := []string{}
	if v, ok := os.LookupEnv("CURSEFORGE_SEARCH_PROXY"); ok {
		proxyURLs = parseProxyList(v) // self-hosted Core API mirror(s)
	}
	return &Client{
		http:       &http.Client{Timeout: 15 * time.Second},
		keyFn:      keyFn,
		coreURL:    defaultCoreURL,
		webURL:     defaultWebURL,
		proxyURLs:  proxyURLs,
		retryDelay: retryBaseDelay,
	}
}

// currentKey returns the active API key, caching the keyFn result for
// keyCacheTTL. With no keyFn (tests) it falls back to the cachedKey field,
// which tests may set directly.
func (c *Client) currentKey() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keyFn == nil {
		return c.cachedKey
	}
	if time.Now().Before(c.keyExpiry) {
		return c.cachedKey
	}
	c.cachedKey = c.keyFn()
	c.keyExpiry = time.Now().Add(keyCacheTTL)
	return c.cachedKey
}

// parseProxyList splits a comma-separated CURSEFORGE_SEARCH_PROXY value into
// trimmed base URLs, dropping empties (so a set-but-empty value disables the
// proxy entirely).
func parseProxyList(v string) []string {
	out := []string{}
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimRight(strings.TrimSpace(p), "/"); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Enabled reports whether search/metadata is available — via the keyed Core
// API or the key-less proxy. Installed-mod version checks and downloads work
// regardless.
func (c *Client) Enabled() bool { return c.currentKey() != "" || len(c.proxyURLs) > 0 }

// keyed reports whether the official Core API is in use. Without a key,
// search/metadata route through the proxy while file listings and downloads
// stay on the anonymous curseforge.com website API.
func (c *Client) keyed() bool { return c.currentKey() != "" }

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

// cfSortField maps the shared sort index to CurseForge's sortField enum
// (2=Popularity, 3=LastUpdated, 6=TotalDownloads, 11=ReleasedDate). CF has no
// relevance or follows ranking, so both fall back to popularity.
func cfSortField(index string) string {
	switch index {
	case "downloads":
		return "6"
	case "newest":
		return "11"
	case "updated":
		return "3"
	default: // relevance, follows
		return "2"
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

// get calls the Core API: keyed api.curseforge.com when an API key is set,
// otherwise the key-less proxy mirror(s) (same paths, same response shapes).
// Configuring more than one proxy lets a transient outage on the first fail
// over to the next.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	if c.keyed() {
		return c.fetchJSON(ctx, []string{c.coreURL}, path, q, true, out)
	}
	return c.fetchJSON(ctx, c.proxyURLs, path, q, false, out)
}

// post sends a JSON body to the Core API (keyed core, else key-less proxy) and
// decodes the response, with the same retry/failover behavior as get. Used by
// the fingerprint and bulk-mod endpoints, which are POST-only.
func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	if !c.Enabled() {
		return ErrDisabled
	}
	bases := c.proxyURLs
	keyed := false
	if c.keyed() {
		bases = []string{c.coreURL}
		keyed = true
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	var lastErr error
	for _, base := range bases {
		u := base + path
		for attempt := 0; ; attempt++ {
			retryable, err := c.doPostOnce(ctx, u, keyed, payload, out)
			if err == nil {
				return nil
			}
			lastErr = err
			if ctx.Err() != nil {
				return err
			}
			if !retryable || attempt+1 >= maxAttempts {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.retryDelay << attempt):
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("curseforge: no endpoint configured")
	}
	return lastErr
}

func (c *Client) doPostOnce(ctx context.Context, u string, keyed bool, payload []byte, out any) (retryable bool, err error) {
	attemptCtx, cancel := context.WithTimeout(ctx, perAttemptTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodPost, u, bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	if keyed {
		req.Header.Set("x-api-key", c.currentKey())
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return retry, fmt.Errorf("curseforge returned %d%s", resp.StatusCode, errSnippet(snippet))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return true, fmt.Errorf("curseforge decode failed: %w", err)
	}
	return false, nil
}

// FingerprintMatch identifies a jar that exactly matches a CurseForge file by its
// murmur2 fingerprint.
type FingerprintMatch struct {
	ModID       int
	FileID      int
	DisplayName string // the file's display name (often a version string)
	FileName    string
}

// MatchFingerprints looks up CurseForge files by murmur2 fingerprint (the same
// mechanism modpack launchers use to identify loose jars), returning a
// fingerprint->match map for exact matches. Requires CurseForge to be enabled
// (an API key or a POST-capable proxy); when it isn't, it returns no matches
// rather than an error so recognition silently no-ops.
func (c *Client) MatchFingerprints(ctx context.Context, fingerprints []uint32) (map[uint32]FingerprintMatch, error) {
	if !c.Enabled() || len(fingerprints) == 0 {
		return map[uint32]FingerprintMatch{}, nil
	}
	var raw struct {
		Data struct {
			ExactMatches []struct {
				ID   int `json:"id"` // mod id
				File struct {
					ID              int    `json:"id"`
					ModID           int    `json:"modId"`
					DisplayName     string `json:"displayName"`
					FileName        string `json:"fileName"`
					FileFingerprint uint32 `json:"fileFingerprint"`
				} `json:"file"`
			} `json:"exactMatches"`
		} `json:"data"`
	}
	if err := c.post(ctx, "/v1/fingerprints", map[string]any{"fingerprints": fingerprints}, &raw); err != nil {
		return nil, err
	}
	out := make(map[uint32]FingerprintMatch, len(raw.Data.ExactMatches))
	for _, m := range raw.Data.ExactMatches {
		modID := m.File.ModID
		if modID == 0 {
			modID = m.ID
		}
		out[m.File.FileFingerprint] = FingerprintMatch{
			ModID:       modID,
			FileID:      m.File.ID,
			DisplayName: m.File.DisplayName,
			FileName:    m.File.FileName,
		}
	}
	return out, nil
}

// GetModNames resolves CurseForge mod ids to their display names in one bulk call
// (POST /v1/mods), used to label recognized mods with the project name rather
// than a file's version string. Best-effort: unknown ids are simply absent.
func (c *Client) GetModNames(ctx context.Context, ids []int) (map[int]string, error) {
	if !c.Enabled() || len(ids) == 0 {
		return map[int]string{}, nil
	}
	var raw struct {
		Data []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := c.post(ctx, "/v1/mods", map[string]any{"modIds": ids}, &raw); err != nil {
		return nil, err
	}
	out := make(map[int]string, len(raw.Data))
	for _, m := range raw.Data {
		out[m.ID] = m.Name
	}
	return out, nil
}

// getWeb calls the anonymous curseforge.com website API. No key needed, but
// only the file endpoints answer without Cloudflare interference; search and
// mod metadata return 403 anonymously and must go through the Core API.
func (c *Client) getWeb(ctx context.Context, path string, q url.Values, out any) error {
	return c.fetchJSON(ctx, []string{c.webURL}, path, q, false, out)
}

// fetchJSON GETs path+query against each base URL in turn, decoding the first
// success into out. Within a base it retries transient failures (network
// errors, 429, 5xx) with exponential backoff; on a non-transient error it
// stops retrying that base but still fails over to the next. The caller's
// context bounds the whole operation; each attempt additionally gets its own
// shorter deadline so a hung request doesn't consume the entire budget.
func (c *Client) fetchJSON(ctx context.Context, bases []string, path string, q url.Values, keyed bool, out any) error {
	var lastErr error
	for _, base := range bases {
		u := base + path
		if len(q) > 0 {
			u += "?" + q.Encode()
		}
		for attempt := 0; ; attempt++ {
			retryable, err := c.doJSONOnce(ctx, u, keyed, out)
			if err == nil {
				return nil
			}
			lastErr = err
			// The caller's deadline trumps everything — don't retry or fail over
			// once it's gone.
			if ctx.Err() != nil {
				return err
			}
			if !retryable || attempt+1 >= maxAttempts {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.retryDelay << attempt):
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("curseforge: no endpoint configured")
	}
	return lastErr
}

// doJSONOnce performs a single GET+decode. It reports whether the failure is
// worth retrying: transport errors and 429/5xx responses are transient; a
// 4xx (other than 429) is the server's final word and is not.
func (c *Client) doJSONOnce(ctx context.Context, u string, keyed bool, out any) (retryable bool, err error) {
	attemptCtx, cancel := context.WithTimeout(ctx, perAttemptTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	if keyed {
		req.Header.Set("x-api-key", c.currentKey())
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return true, err // network/transport error (incl. attempt timeout)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		retry := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return retry, fmt.Errorf("curseforge returned %d%s", resp.StatusCode, errSnippet(snippet))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return true, fmt.Errorf("curseforge decode failed: %w", err)
	}
	return false, nil
}

// errSnippet formats a short, single-line excerpt of an error response body
// for inclusion in an error message, or "" when the body is empty.
func errSnippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	return ": " + s
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
	Authors []struct {
		Name string `json:"name"`
	} `json:"authors"`
	LatestFiles        []cfFile `json:"latestFiles"`
	LatestFilesIndexes []struct {
		GameVersion string `json:"gameVersion"`
	} `json:"latestFilesIndexes"`
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

// cfWebFile is the file shape served by the website API. Unlike the Core API
// it carries no downloadUrl and no hashes; gameVersions mixes loader names
// ("Fabric", "NeoForge") with Minecraft versions ("1.21.4").
type cfWebFile struct {
	ID           int      `json:"id"`
	DisplayName  string   `json:"displayName"`
	FileName     string   `json:"fileName"`
	GameVersions []string `json:"gameVersions"`
}

// ── normalized API ───────────────────────────────────────────────────

func (c *Client) Search(ctx context.Context, p modrinth.SearchParams) (*modrinth.SearchResult, error) {
	q := url.Values{
		"gameId":    {fmt.Sprint(minecraftGame)},
		"classId":   {fmt.Sprint(classID(p.ProjectType))},
		"pageSize":  {fmt.Sprint(orDefault(p.Limit, 20))},
		"index":     {fmt.Sprint(p.Offset)},
		"sortField": {cfSortField(p.Index)},
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
	// Category filters arrive as CF numeric category ids (from GetCategories);
	// anything non-numeric (e.g. a Modrinth tag name) is ignored. Multiple ids
	// AND together, mirroring Modrinth's facet semantics.
	if ids := numericIDs(p.Categories); len(ids) == 1 {
		q.Set("categoryId", ids[0])
	} else if len(ids) > 1 {
		q.Set("categoryIds", "["+strings.Join(ids, ",")+"]")
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
			Author:      cfAuthor(m),
			Description: m.Summary,
			Categories:  categoryNames(m),
			Downloads:   m.DownloadCount,
			IconURL:     m.Logo.URL,
			ProjectType: orString(p.ProjectType, "mod"),
			Versions:    gameVersions(m),
		})
	}
	return &modrinth.SearchResult{
		Hits:  hits,
		Total: raw.Pagination.TotalCount,
		Limit: orDefault(p.Limit, 20),
	}, nil
}

// GetCategories lists the CurseForge categories for a project type's class,
// normalized into the Modrinth category shape. ID carries the numeric CF
// category id Search filters by; Icon is an image URL rather than inline SVG
// (the frontend renders both). Mods have a two-tier taxonomy: top-level
// categories (parent == class) land under "categories", sub-categories of
// other mods (Create addons, Blood Magic, …) under "addons".
func (c *Client) GetCategories(ctx context.Context, projectType string) ([]modrinth.Category, error) {
	q := url.Values{
		"gameId":  {fmt.Sprint(minecraftGame)},
		"classId": {fmt.Sprint(classID(projectType))},
	}
	var raw struct {
		Data []struct {
			ID               int    `json:"id"`
			Name             string `json:"name"`
			IconURL          string `json:"iconUrl"`
			ClassID          int    `json:"classId"`
			ParentCategoryID int    `json:"parentCategoryId"`
		} `json:"data"`
	}
	if err := c.get(ctx, "/v1/categories", q, &raw); err != nil {
		return nil, err
	}
	out := make([]modrinth.Category, 0, len(raw.Data))
	for _, cat := range raw.Data {
		header := "categories"
		if cat.ParentCategoryID != 0 && cat.ParentCategoryID != cat.ClassID {
			header = "addons"
		}
		out = append(out, modrinth.Category{
			ID:          fmt.Sprint(cat.ID),
			Icon:        cat.IconURL,
			Name:        cat.Name,
			ProjectType: orString(projectType, "mod"),
			Header:      header,
		})
	}
	// CF returns categories unordered; sort top-level first, then addons,
	// alphabetical within each group.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Header != out[j].Header {
			return out[i].Header == "categories"
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
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
	// Key-less file listings stay on the official website API rather than the
	// proxy: installs and update checks shouldn't depend on a third party.
	if !c.keyed() {
		return c.webVersions(ctx, projectID, loader, mcVersion)
	}
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
		versions = append(versions, c.fileToVersion(projectID, loader, f))
	}
	return versions, nil
}

func (c *Client) GetVersion(ctx context.Context, projectID, fileID string) (*modrinth.Version, error) {
	var v *modrinth.Version
	if !c.keyed() {
		wv, err := c.webVersion(ctx, projectID, fileID)
		if err != nil {
			return nil, err
		}
		v = wv
	} else {
		var raw struct {
			Data cfFile `json:"data"`
		}
		if err := c.get(ctx, "/v1/mods/"+projectID+"/files/"+fileID, nil, &raw); err != nil {
			return nil, err
		}
		cv := c.fileToVersion(projectID, "", raw.Data)
		v = &cv
	}
	// Changelog lives on a separate endpoint; failures (or a disabled proxy)
	// are non-fatal — the UI just shows "No changelog provided".
	if cl, err := c.changelog(ctx, projectID, fileID); err == nil {
		v.Changelog = cl
	}
	return v, nil
}

// webVersions lists a mod's files through the anonymous website API and
// filters by loader/game version client-side (the endpoint has no equivalent
// of the Core API's modLoaderType/gameVersion parameters). Files come back
// newest-first, matching the Core API ordering update checks rely on.
func (c *Client) webVersions(ctx context.Context, projectID, loader, mcVersion string) ([]modrinth.Version, error) {
	q := url.Values{
		"pageIndex": {"0"},
		"pageSize":  {"50"},
	}
	var raw struct {
		Data []cfWebFile `json:"data"`
	}
	if err := c.getWeb(ctx, "/api/v1/mods/"+projectID+"/files", q, &raw); err != nil {
		return nil, err
	}
	versions := make([]modrinth.Version, 0, len(raw.Data))
	for _, f := range raw.Data {
		if !containsFold(f.GameVersions, loader) || !contains(f.GameVersions, mcVersion) {
			continue
		}
		versions = append(versions, c.webFileToVersion(projectID, loader, f))
	}
	return versions, nil
}

func (c *Client) webVersion(ctx context.Context, projectID, fileID string) (*modrinth.Version, error) {
	var raw struct {
		Data cfWebFile `json:"data"`
	}
	if err := c.getWeb(ctx, "/api/v1/mods/"+projectID+"/files/"+fileID, nil, &raw); err != nil {
		return nil, err
	}
	v := c.webFileToVersion(projectID, "", raw.Data)
	return &v, nil
}

func (c *Client) fileToVersion(projectID, loader string, f cfFile) modrinth.Version {
	loaders := []string{}
	if loader != "" {
		loaders = []string{loader}
	}
	dl := f.DownloadURL
	if dl == "" {
		// Authors can disable third-party API downloads, which nulls
		// downloadUrl; the website redirect endpoint still serves the file.
		dl = c.websiteDownloadURL(projectID, f.ID)
	}
	vf := modrinth.VersionFile{
		URL:      dl,
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

func (c *Client) webFileToVersion(projectID, loader string, f cfWebFile) modrinth.Version {
	loaders := []string{}
	if loader != "" {
		loaders = []string{loader}
	}
	vf := modrinth.VersionFile{
		URL:      c.websiteDownloadURL(projectID, f.ID),
		Filename: f.FileName,
		Primary:  true,
	}
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

// websiteDownloadURL returns the anonymous website redirect endpoint for a
// file. GET answers 307 to the forgecdn CDN, which http.Client follows
// transparently, so the URL can stand in anywhere a direct downloadUrl would.
func (c *Client) websiteDownloadURL(projectID string, fileID int) string {
	return fmt.Sprintf("%s/api/v1/mods/%s/files/%d/download", c.webURL, projectID, fileID)
}

// gameVersions collects the Minecraft versions covered by a mod's latest
// files so search hits carry the same versions list Modrinth hits do (the
// frontend's compatibility badge reads it and must never see null).
// latestFilesIndexes has one clean gameVersion per (version, loader) pair;
// latestFiles' gameVersions mixes loader/environment names in, so only
// entries starting with a digit count as versions.
func gameVersions(m cfMod) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(v string) {
		if v == "" || v[0] < '0' || v[0] > '9' || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	for _, idx := range m.LatestFilesIndexes {
		add(idx.GameVersion)
	}
	for _, f := range m.LatestFiles {
		for _, gv := range f.GameVersions {
			add(gv)
		}
	}
	return out
}

// cfAuthor returns the primary author name, or "" when none is listed.
func cfAuthor(m cfMod) string {
	if len(m.Authors) > 0 {
		return m.Authors[0].Name
	}
	return ""
}

func categoryNames(m cfMod) []string {
	out := make([]string, 0, len(m.Categories))
	for _, c := range m.Categories {
		out = append(out, c.Name)
	}
	return out
}

// numericIDs keeps only the entries that parse as integers.
func numericIDs(list []string) []string {
	out := []string{}
	for _, v := range list {
		if _, err := strconv.Atoi(v); err == nil && v != "" {
			out = append(out, v)
		}
	}
	return out
}

// contains reports whether list has an exact entry; an empty want matches.
func contains(list []string, want string) bool {
	if want == "" {
		return true
	}
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// containsFold is contains with case-insensitive comparison ("neoforge"
// matches CurseForge's "NeoForge" gameVersions entry).
func containsFold(list []string, want string) bool {
	if want == "" {
		return true
	}
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
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
