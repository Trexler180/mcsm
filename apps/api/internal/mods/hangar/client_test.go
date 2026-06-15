package hangar

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/mcsm/api/internal/mods/modrinth"
)

func testClient(baseURL string) *Client {
	return &Client{
		http:    &http.Client{Timeout: 5 * time.Second},
		baseURL: baseURL,
	}
}

func TestSearch(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"pagination":{"limit":20,"offset":0,"count":42},"result":[
			{"id":12,"name":"ViaBackwards","namespace":{"owner":"ViaVersion","slug":"ViaBackwards"},
			 "description":"Allow older clients","category":"misc","lastUpdated":"2025-05-01T00:00:00Z",
			 "avatarUrl":"https://hangarcdn.papermc.io/avatars/12.webp",
			 "stats":{"downloads":663000,"stars":321},
			 "supportedPlatforms":{"PAPER":["1.20","1.21.4"]}}
		]}`))
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	res, err := c.Search(context.Background(), modrinth.SearchParams{
		Query: "via", MCVersion: "1.21.4", Categories: []string{"misc"}, Index: "downloads",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Platform must always accompany the version filter (Hangar 400s otherwise).
	if got := gotQuery.Get("platform"); got != "PAPER" {
		t.Errorf("want platform=PAPER, got %q", got)
	}
	if got := gotQuery.Get("version"); got != "1.21.4" {
		t.Errorf("want version=1.21.4, got %q", got)
	}
	if got := gotQuery.Get("q"); got != "via" {
		t.Errorf("want q=via, got %q", got)
	}
	if got := gotQuery.Get("sort"); got != "-downloads" {
		t.Errorf("want sort=-downloads, got %q", got)
	}
	if got := gotQuery.Get("category"); got != "misc" {
		t.Errorf("want category=misc, got %q", got)
	}
	if res.Total != 42 {
		t.Fatalf("want total 42, got %d", res.Total)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("want 1 hit, got %d", len(res.Hits))
	}
	h := res.Hits[0]
	if h.ProjectID != "12" {
		t.Errorf("ProjectID must be the numeric id, got %q", h.ProjectID)
	}
	if h.Slug != "ViaVersion/ViaBackwards" {
		t.Errorf("Slug must be owner/slug for web links, got %q", h.Slug)
	}
	if h.Author != "ViaVersion" || h.ProjectType != "plugin" || h.Downloads != 663000 {
		t.Errorf("wrong hit mapping: %+v", h)
	}
	if len(h.Versions) != 2 || h.Versions[1] != "1.21.4" {
		t.Errorf("versions must come from supportedPlatforms.PAPER, got %v", h.Versions)
	}
}

func TestSearchSortMapping(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"pagination":{"count":0},"result":[]}`))
	}))
	defer srv.Close()
	c := testClient(srv.URL)

	for index, want := range map[string]string{
		"downloads": "-downloads",
		"follows":   "-stars",
		"newest":    "-newest",
		"updated":   "-updated",
		"":          "-downloads", // browse default
	} {
		if _, err := c.Search(context.Background(), modrinth.SearchParams{Index: index}); err != nil {
			t.Fatal(err)
		}
		if got := gotQuery.Get("sort"); got != want {
			t.Errorf("index %q: want sort %q, got %q", index, want, got)
		}
	}
	// Relevance with a query leaves the ordering to Hangar.
	if _, err := c.Search(context.Background(), modrinth.SearchParams{Query: "x", Index: "relevance"}); err != nil {
		t.Fatal(err)
	}
	if gotQuery.Has("sort") {
		t.Error("relevance with query must not send a sort")
	}
}

func TestGetProject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects/12" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":12,"name":"ViaBackwards","namespace":{"owner":"ViaVersion","slug":"ViaBackwards"},
			"description":"Allow older clients","category":"misc","lastUpdated":"2025-05-01T00:00:00Z",
			"avatarUrl":"https://hangarcdn.papermc.io/avatars/12.webp",
			"stats":{"downloads":663000,"stars":321},
			"supportedPlatforms":{"PAPER":["1.21.4"]},
			"mainPageContent":"# ViaBackwards\nbody here",
			"settings":{"links":[{"links":[
				{"name":"Source Code","url":"https://github.com/ViaVersion/ViaBackwards"},
				{"name":"Issue Tracker","url":"https://github.com/ViaVersion/ViaBackwards/issues"},
				{"name":"Documentation","url":"https://viaversion.com/docs"}
			]}]}}`))
	}))
	defer srv.Close()

	p, err := testClient(srv.URL).GetProject(context.Background(), "12")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "12" || p.Slug != "ViaVersion/ViaBackwards" || p.Body != "# ViaBackwards\nbody here" {
		t.Fatalf("wrong project mapping: %+v", p)
	}
	if p.Followers != 321 {
		t.Errorf("stars must map to followers, got %d", p.Followers)
	}
	if p.SourceURL == nil || *p.SourceURL != "https://github.com/ViaVersion/ViaBackwards" {
		t.Errorf("source link not matched: %v", p.SourceURL)
	}
	if p.IssuesURL == nil || *p.IssuesURL != "https://github.com/ViaVersion/ViaBackwards/issues" {
		t.Errorf("issues link not matched: %v", p.IssuesURL)
	}
	if p.WikiURL == nil || *p.WikiURL != "https://viaversion.com/docs" {
		t.Errorf("docs link not matched: %v", p.WikiURL)
	}
}

const versionJSON = `{
	"id":26353,"name":"5.0.3","createdAt":"2025-04-01T00:00:00Z",
	"description":"## Fixed\n- a bug",
	"channel":{"name":"Release"},
	"downloads":{"PAPER":{
		"fileInfo":{"name":"ViaBackwards-5.0.3.jar","sizeBytes":1048576,"sha256Hash":"abc123"},
		"externalUrl":null,
		"downloadUrl":"https://hangar.papermc.io/api/v1/projects/ViaBackwards/versions/5.0.3/PAPER/download"}},
	"pluginDependencies":{"PAPER":[
		{"name":"ViaVersion","projectId":7,"required":true},
		{"name":"SomeExternal","projectId":0,"required":true}]},
	"platformDependencies":{"PAPER":["1.20","1.21","1.21.4"]}
}`

func TestGetVersions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects/12/versions" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"pagination":{"count":2},"result":[` + versionJSON + `,
			{"id":26000,"name":"5.0.2","createdAt":"2025-03-01T00:00:00Z","channel":{"name":"Snapshot"},
			 "downloads":{"PAPER":{"fileInfo":{"name":"ViaBackwards-5.0.2.jar"},"downloadUrl":"https://x/dl"}},
			 "platformDependencies":{"PAPER":["1.20"]}}]}`))
	}))
	defer srv.Close()

	c := testClient(srv.URL)
	all, err := c.GetVersions(context.Background(), "12", "paper", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 versions, got %d", len(all))
	}
	v := all[0]
	if v.ID != "26353" || v.ProjectID != "12" || v.VersionNumber != "5.0.3" {
		t.Fatalf("wrong version mapping: %+v", v)
	}
	if v.VersionType != "release" || all[1].VersionType != "alpha" {
		t.Errorf("channel mapping wrong: %q / %q", v.VersionType, all[1].VersionType)
	}
	if v.Changelog != "## Fixed\n- a bug" {
		t.Errorf("changelog must come from description, got %q", v.Changelog)
	}
	if len(v.Files) != 1 || v.Files[0].Filename != "ViaBackwards-5.0.3.jar" || v.Files[0].Hashes.SHA256 != "abc123" {
		t.Fatalf("wrong file mapping: %+v", v.Files)
	}
	// External dependency (projectId 0) dropped, Hangar dependency kept.
	if len(v.Dependencies) != 1 || v.Dependencies[0].ProjectID != "7" || v.Dependencies[0].DependencyType != "required" {
		t.Fatalf("wrong dependencies: %+v", v.Dependencies)
	}

	// mcVersion filters client-side; "1.21.4" matches the first version only.
	filtered, err := c.GetVersions(context.Background(), "12", "paper", "1.21.4")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].ID != "26353" {
		t.Fatalf("want only 26353 for 1.21.4, got %+v", filtered)
	}
	// A major-line declaration ("1.20") covers its patch releases.
	patch, err := c.GetVersions(context.Background(), "12", "paper", "1.20.6")
	if err != nil {
		t.Fatal(err)
	}
	if len(patch) != 2 {
		t.Fatalf("major-line 1.20 must match 1.20.6, got %d versions", len(patch))
	}
}

func TestGetVersionExternalDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/projects/12/versions/26353" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":26353,"name":"5.0.3","channel":{"name":"Release"},
			"downloads":{"PAPER":{"fileInfo":{"name":"","sizeBytes":0,"sha256Hash":""},
			"externalUrl":"https://example.com/file.jar","downloadUrl":""}},
			"platformDependencies":{"PAPER":["1.21.4"]}}`))
	}))
	defer srv.Close()

	v, err := testClient(srv.URL).GetVersion(context.Background(), "12", "26353")
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Files) != 1 || v.Files[0].URL != "https://example.com/file.jar" {
		t.Fatalf("external URL must back missing download URL: %+v", v.Files)
	}
	if v.Files[0].Filename != "5.0.3.jar" {
		t.Errorf("missing filename must fall back to version name, got %q", v.Files[0].Filename)
	}
	if v.Files[0].Hashes.SHA256 != "" {
		t.Error("external files must not claim a hash")
	}
}

func TestGetCategories(t *testing.T) {
	c := testClient("http://invalid.localhost")
	cats, err := c.GetCategories(context.Background(), "plugin")
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) != 10 || cats[0].ID != "admin_tools" || cats[0].Name != "Admin Tools" {
		t.Fatalf("wrong categories: %+v", cats)
	}
	// Hangar only hosts plugins; other project types have no categories.
	empty, err := c.GetCategories(context.Background(), "mod")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("want no categories for mods, got %d", len(empty))
	}
}
