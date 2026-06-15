package curseforge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/mcsm/api/internal/mods/modrinth"
)

func testClient(apiKey, coreURL, webURL, proxyURL string) *Client {
	// keyFn is nil here, so currentKey() returns the cachedKey field directly —
	// a stable static key for the lifetime of the test client.
	return &Client{
		http:       &http.Client{Timeout: 5 * time.Second},
		cachedKey:  apiKey,
		coreURL:    coreURL,
		webURL:     webURL,
		proxyURLs:  parseProxyList(proxyURL),
		retryDelay: time.Millisecond, // keep retry-path tests fast
	}
}

func TestKeylessSearchDisabledWithoutProxy(t *testing.T) {
	c := testClient("", "http://invalid.localhost", "http://invalid.localhost", "")
	if c.Enabled() {
		t.Fatal("client without key or proxy must report disabled")
	}
	if _, err := c.Search(context.Background(), modrinth.SearchParams{Query: "lithium"}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("want ErrDisabled, got %v", err)
	}
	if _, err := c.GetProject(context.Background(), "360438"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("want ErrDisabled, got %v", err)
	}
}

func TestKeylessSearchUsesProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/mods/search" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") != "" {
			t.Error("proxy request must not carry an api key")
		}
		if got := r.URL.Query().Get("searchFilter"); got != "lithium" {
			t.Errorf("want searchFilter=lithium, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[
			{"id":360438,"slug":"lithium","name":"Lithium","summary":"Performance mod","downloadCount":42,"authors":[{"name":"someone"}],
			 "latestFiles":[{"id":1,"fileName":"a.jar","gameVersions":["Fabric","Client","1.20.1"]}],
			 "latestFilesIndexes":[{"gameVersion":"1.21.4","fileId":1,"modLoader":4},{"gameVersion":"1.21.4","fileId":2,"modLoader":6}]}
		],"pagination":{"totalCount":1}}`))
	}))
	defer proxy.Close()

	c := testClient("", "http://invalid.localhost", "http://invalid.localhost", proxy.URL)
	if !c.Enabled() {
		t.Fatal("client with proxy must report enabled")
	}
	res, err := c.Search(context.Background(), modrinth.SearchParams{Query: "lithium"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].ProjectID != "360438" || res.Hits[0].Slug != "lithium" {
		t.Fatalf("wrong hits: %+v", res.Hits)
	}
	if res.Total != 1 {
		t.Fatalf("want total 1, got %d", res.Total)
	}
	// Game versions deduped across latestFilesIndexes + latestFiles, loader and
	// environment names dropped; never nil (the frontend calls .includes on it).
	if got := res.Hits[0].Versions; len(got) != 2 || got[0] != "1.21.4" || got[1] != "1.20.1" {
		t.Fatalf("want versions [1.21.4 1.20.1], got %v", got)
	}
}

// Browse (no query) works, sort indexes map to CF sortField, and category ids
// pass through; non-numeric (Modrinth-style) category names are dropped.
func TestSearchSortAndCategories(t *testing.T) {
	var gotQuery url.Values
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[],"pagination":{"totalCount":0}}`))
	}))
	defer proxy.Close()

	c := testClient("", "http://invalid.localhost", "http://invalid.localhost", proxy.URL)

	// Empty query = browse; must not send a searchFilter.
	if _, err := c.Search(context.Background(), modrinth.SearchParams{Index: "downloads"}); err != nil {
		t.Fatal(err)
	}
	if gotQuery.Has("searchFilter") {
		t.Error("browse must not send searchFilter")
	}
	if got := gotQuery.Get("sortField"); got != "6" {
		t.Errorf("downloads must map to sortField 6, got %q", got)
	}

	for index, want := range map[string]string{"updated": "3", "newest": "11", "relevance": "2", "": "2"} {
		if _, err := c.Search(context.Background(), modrinth.SearchParams{Index: index}); err != nil {
			t.Fatal(err)
		}
		if got := gotQuery.Get("sortField"); got != want {
			t.Errorf("index %q: want sortField %s, got %q", index, want, got)
		}
	}

	if _, err := c.Search(context.Background(), modrinth.SearchParams{Categories: []string{"434", "utility"}}); err != nil {
		t.Fatal(err)
	}
	if got := gotQuery.Get("categoryId"); got != "434" {
		t.Errorf("single numeric category must use categoryId, got %q", got)
	}

	if _, err := c.Search(context.Background(), modrinth.SearchParams{Categories: []string{"434", "435"}}); err != nil {
		t.Fatal(err)
	}
	if got := gotQuery.Get("categoryIds"); got != "[434,435]" {
		t.Errorf("multiple categories must use categoryIds, got %q", got)
	}
}

func TestGetCategories(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/categories" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("classId"); got != "6" {
			t.Errorf("want classId 6 for mods, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[
			{"id":6484,"name":"Create","iconUrl":"https://cdn/create.png","classId":6,"parentCategoryId":426},
			{"id":434,"name":"Armor, Tools, and Weapons","iconUrl":"https://cdn/armor.png","classId":6,"parentCategoryId":6},
			{"id":435,"name":"Adventure and RPG","iconUrl":"https://cdn/rpg.png","classId":6,"parentCategoryId":6}
		]}`))
	}))
	defer proxy.Close()

	c := testClient("", "http://invalid.localhost", "http://invalid.localhost", proxy.URL)
	cats, err := c.GetCategories(context.Background(), "mod")
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) != 3 {
		t.Fatalf("want 3 categories, got %d", len(cats))
	}
	// Top-level categories sort first (alphabetical), addon sub-categories after.
	if cats[0].Name != "Adventure and RPG" || cats[0].ID != "435" || cats[0].Header != "categories" {
		t.Fatalf("wrong first category: %+v", cats[0])
	}
	if cats[1].Name != "Armor, Tools, and Weapons" || cats[1].Header != "categories" {
		t.Fatalf("wrong second category: %+v", cats[1])
	}
	if cats[2].Name != "Create" || cats[2].Header != "addons" {
		t.Fatalf("addon sub-category must land under addons header: %+v", cats[2])
	}
	if cats[0].Icon != "https://cdn/rpg.png" || cats[0].ProjectType != "mod" {
		t.Fatalf("wrong icon/projectType: %+v", cats[0])
	}
}

// A hit with no files still serializes versions as [] rather than null.
func TestSearchHitVersionsNeverNil(t *testing.T) {
	if vs := gameVersions(cfMod{}); vs == nil {
		t.Fatal("gameVersions must return a non-nil slice")
	}
}

// With a key set, search must go to the keyed Core API even when a proxy is
// configured.
func TestKeyedSearchSkipsProxy(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret" {
			t.Errorf("missing api key header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[],"pagination":{"totalCount":0}}`))
	}))
	defer core.Close()

	c := testClient("secret", core.URL, "http://invalid.localhost", "http://invalid.localhost")
	if _, err := c.Search(context.Background(), modrinth.SearchParams{Query: "lithium"}); err != nil {
		t.Fatal(err)
	}
}

func TestKeylessGetVersionsUsesWebsiteAndFilters(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/mods/360438/files" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("x-api-key") != "" {
			t.Error("website request must not carry an api key")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[
			{"id":3,"displayName":"Lithium 0.3 Fabric","fileName":"lithium-fabric-0.3.jar","gameVersions":["Fabric","Quilt","1.21.4"]},
			{"id":2,"displayName":"Lithium 0.3 NeoForge","fileName":"lithium-neoforge-0.3.jar","gameVersions":["NeoForge","1.21.4"]},
			{"id":1,"displayName":"Lithium 0.2 Fabric","fileName":"lithium-fabric-0.2.jar","gameVersions":["Fabric","1.20.1"]}
		],"pagination":{"index":0,"pageSize":50,"totalCount":3}}`))
	}))
	defer web.Close()

	// Proxy configured but file listings must still use the website API.
	c := testClient("", "http://invalid.localhost", web.URL, "http://invalid.localhost")
	versions, err := c.GetVersions(context.Background(), "360438", "fabric", "1.21.4")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Fatalf("want 1 version after loader+mc filter, got %d: %+v", len(versions), versions)
	}
	v := versions[0]
	if v.ID != "3" || v.ProjectID != "360438" || v.VersionNumber != "lithium-fabric-0.3.jar" {
		t.Fatalf("wrong version: %+v", v)
	}
	wantURL := web.URL + "/api/v1/mods/360438/files/3/download"
	if len(v.Files) != 1 || v.Files[0].URL != wantURL {
		t.Fatalf("want file URL %s, got %+v", wantURL, v.Files)
	}
	if v.Files[0].Hashes.SHA256 != "" {
		t.Fatalf("website files have no hashes; SHA256 must stay empty, got %q", v.Files[0].Hashes.SHA256)
	}
}

func TestKeylessGetVersionUsesWebsite(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/mods/360438/files/8196298" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"id":8196298,"displayName":"Lithium 0.24.5","fileName":"lithium-fabric-0.24.5.jar","gameVersions":["Fabric","26.1.2"]}}`))
	}))
	defer web.Close()

	c := testClient("", "http://invalid.localhost", web.URL, "http://invalid.localhost")
	v, err := c.GetVersion(context.Background(), "360438", "8196298")
	if err != nil {
		t.Fatal(err)
	}
	wantURL := web.URL + "/api/v1/mods/360438/files/8196298/download"
	if v.ID != "8196298" || len(v.Files) != 1 || v.Files[0].URL != wantURL {
		t.Fatalf("wrong version: %+v", v)
	}
}

// Key-less GetVersion pulls the file from the website API and the changelog
// from the proxy, converting CF's HTML to Markdown. A dead changelog endpoint
// must not fail the version fetch (covered by TestKeylessGetVersionUsesWebsite,
// whose proxy URL is unreachable).
func TestGetVersionFetchesChangelog(t *testing.T) {
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"id":8196298,"displayName":"Lithium 0.24.5","fileName":"lithium-fabric-0.24.5.jar","gameVersions":["Fabric","26.1.2"]}}`))
	}))
	defer web.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/mods/360438/files/8196298/changelog" {
			t.Errorf("unexpected proxy path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":"<h2>Fixes</h2>\n<ul>\n<li>Fix <strong>crash</strong> on boot</li>\n<li></li>\n</ul>\n"}`))
	}))
	defer proxy.Close()

	c := testClient("", "http://invalid.localhost", web.URL, proxy.URL)
	v, err := c.GetVersion(context.Background(), "360438", "8196298")
	if err != nil {
		t.Fatal(err)
	}
	want := "## Fixes\n\n- Fix **crash** on boot"
	if v.Changelog != want {
		t.Fatalf("want changelog %q, got %q", want, v.Changelog)
	}
}

// A transient 503 on search is retried and eventually succeeds rather than
// surfacing the blip to the caller.
func TestSearchRetriesTransientError(t *testing.T) {
	var calls int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			http.Error(w, "upstream hiccup", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[],"pagination":{"totalCount":0}}`))
	}))
	defer proxy.Close()

	c := testClient("", "http://invalid.localhost", "http://invalid.localhost", proxy.URL)
	if _, err := c.Search(context.Background(), modrinth.SearchParams{Query: "lithium"}); err != nil {
		t.Fatalf("search should succeed after retries, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("want 3 attempts (2 failures + success), got %d", calls)
	}
}

// A 4xx response is the server's final answer; it must not be retried.
func TestSearchDoesNotRetryClientError(t *testing.T) {
	var calls int
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer proxy.Close()

	c := testClient("", "http://invalid.localhost", "http://invalid.localhost", proxy.URL)
	if _, err := c.Search(context.Background(), modrinth.SearchParams{Query: "lithium"}); err == nil {
		t.Fatal("want error from 400 response")
	}
	if calls != 1 {
		t.Fatalf("400 must not be retried, got %d attempts", calls)
	}
}

// With multiple proxies configured, a dead primary fails over to the backup.
func TestSearchFailsOverToSecondProxy(t *testing.T) {
	var primaryCalls, backupCalls int
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls++
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backupCalls++
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[],"pagination":{"totalCount":0}}`))
	}))
	defer backup.Close()

	c := testClient("", "http://invalid.localhost", "http://invalid.localhost", primary.URL+","+backup.URL)
	if _, err := c.Search(context.Background(), modrinth.SearchParams{Query: "lithium"}); err != nil {
		t.Fatalf("search should fail over to backup, got %v", err)
	}
	if primaryCalls != maxAttempts {
		t.Fatalf("primary should be retried %d times before failover, got %d", maxAttempts, primaryCalls)
	}
	if backupCalls != 1 {
		t.Fatalf("backup should answer once, got %d", backupCalls)
	}
}

// With a key, a null Core API downloadUrl (author disabled API downloads)
// falls back to the website redirect endpoint instead of an empty URL.
func TestKeyedNullDownloadURLFallsBackToWebsite(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "secret" {
			t.Errorf("missing api key header")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/mods/123/files":
			w.Write([]byte(`{"data":[
				{"id":11,"displayName":"With URL","fileName":"a.jar","downloadUrl":"https://edge.example/a.jar","gameVersions":["Fabric","1.21.4"]},
				{"id":12,"displayName":"API downloads off","fileName":"b.jar","downloadUrl":null,"gameVersions":["Fabric","1.21.4"]}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer core.Close()

	c := testClient("secret", core.URL, "https://web.example", "")
	versions, err := c.GetVersions(context.Background(), "123", "fabric", "1.21.4")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("want 2 versions, got %d", len(versions))
	}
	if got := versions[0].Files[0].URL; got != "https://edge.example/a.jar" {
		t.Fatalf("direct URL must be kept, got %s", got)
	}
	if got := versions[1].Files[0].URL; got != "https://web.example/api/v1/mods/123/files/12/download" {
		t.Fatalf("null downloadUrl must fall back to website redirect, got %s", got)
	}
}

// A keyFn that returns a key makes the client keyed and enabled, and the key is
// sent as x-api-key on Core API requests.
func TestKeyFnEnablesKeyedSearch(t *testing.T) {
	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "from-store" {
			t.Errorf("x-api-key = %q, want key from keyFn", r.Header.Get("x-api-key"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[],"pagination":{"totalCount":0}}`))
	}))
	defer core.Close()

	c := New(func() string { return "from-store" })
	c.coreURL = core.URL
	c.retryDelay = time.Millisecond
	if !c.Enabled() || !c.keyed() {
		t.Fatalf("Enabled=%v keyed=%v, want both true", c.Enabled(), c.keyed())
	}
	if _, err := c.Search(context.Background(), modrinth.SearchParams{Query: "lithium"}); err != nil {
		t.Fatal(err)
	}
}

// No key and no proxy → search is disabled (the UI shows an "add a key" prompt).
func TestEmptyKeyFnNoProxyDisabled(t *testing.T) {
	t.Setenv("CURSEFORGE_SEARCH_PROXY", "") // set-but-empty: no proxy
	c := New(func() string { return "" })
	if c.Enabled() {
		t.Fatal("Enabled() = true with no key and no proxy, want false")
	}
	if _, err := c.Search(context.Background(), modrinth.SearchParams{Query: "x"}); !errors.Is(err, ErrDisabled) {
		t.Fatalf("Search error = %v, want ErrDisabled", err)
	}
}

// The keyFn result is cached for keyCacheTTL: a second call within the window
// reuses the first value rather than re-invoking keyFn.
func TestCurrentKeyCaches(t *testing.T) {
	calls := 0
	c := New(func() string {
		calls++
		return "k"
	})
	_ = c.currentKey()
	_ = c.currentKey()
	if calls != 1 {
		t.Fatalf("keyFn called %d times, want 1 (cached)", calls)
	}
}
