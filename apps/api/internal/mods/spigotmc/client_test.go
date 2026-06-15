package spigotmc

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/mcsm/api/internal/mods/modrinth"
)

func testClient(baseURL, webURL string) *Client {
	return &Client{
		http:    &http.Client{Timeout: 5 * time.Second},
		baseURL: baseURL,
		webURL:  webURL,
	}
}

const resourceJSON = `{
	"id":1997,"name":"ProtocolLib","tag":"Provides read/write access to packets",
	"testedVersions":["1.20","1.21"],
	"icon":{"url":"data/resource_icons/1/1997.jpg"},
	"premium":false,"external":false,
	"file":{"type":".jar"},
	"version":{"id":500001},
	"likes":1234,"downloads":987654,
	"releaseDate":1357027200,"updateDate":1714521600
}`

func TestSearchByQuery(t *testing.T) {
	var gotQuery url.Values
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("X-Page-Count", "68")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[` + resourceJSON + `]`))
	}))
	defer srv.Close()

	c := testClient(srv.URL, "https://web.example")
	res, err := c.Search(context.Background(), modrinth.SearchParams{
		Query: "protocol", Index: "downloads", Limit: 10, Offset: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/search/resources/protocol" {
		t.Errorf("unexpected path %s", gotPath)
	}
	if got := gotQuery.Get("field"); got != "name" {
		t.Errorf("want field=name, got %q", got)
	}
	// Spiget pages are 1-based: offset 20 at size 10 = page 3.
	if got := gotQuery.Get("page"); got != "3" {
		t.Errorf("want page=3, got %q", got)
	}
	if got := gotQuery.Get("sort"); got != "-downloads" {
		t.Errorf("want sort=-downloads, got %q", got)
	}
	// X-Page-Count counts pages; total = pages × size.
	if res.Total != 680 {
		t.Fatalf("want total 680, got %d", res.Total)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("want 1 hit, got %d", len(res.Hits))
	}
	h := res.Hits[0]
	if h.ProjectID != "1997" || h.Title != "ProtocolLib" || h.Description != "Provides read/write access to packets" {
		t.Fatalf("wrong hit mapping: %+v", h)
	}
	if h.IconURL != "https://web.example/data/resource_icons/1/1997.jpg" {
		t.Errorf("icon must be prefixed with the website URL, got %q", h.IconURL)
	}
	if h.ProjectType != "plugin" || len(h.Versions) != 2 {
		t.Errorf("wrong type/versions: %+v", h)
	}
}

func TestSearchNoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // Spiget 404s when a search matches nothing
	}))
	defer srv.Close()

	res, err := testClient(srv.URL, "").Search(context.Background(), modrinth.SearchParams{Query: "zzzz"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 0 || res.Total != 0 {
		t.Fatalf("want empty result, got %+v", res)
	}
}

func TestSearchBrowseAndCategory(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("X-Page-Count", "1")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := testClient(srv.URL, "")

	// Plain browse lists free resources.
	if _, err := c.Search(context.Background(), modrinth.SearchParams{Index: "updated"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/resources/free" {
		t.Errorf("browse must hit /resources/free, got %s", gotPath)
	}
	if got := gotQuery.Get("sort"); got != "-updateDate" {
		t.Errorf("want sort=-updateDate, got %q", got)
	}

	// A numeric category filter browses that category.
	if _, err := c.Search(context.Background(), modrinth.SearchParams{Categories: []string{"4"}, Index: "follows"}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/categories/4/resources" {
		t.Errorf("category browse must hit /categories/4/resources, got %s", gotPath)
	}
	if got := gotQuery.Get("sort"); got != "-likes" {
		t.Errorf("follows must map to -likes, got %q", got)
	}
}

func TestGetProject(t *testing.T) {
	desc := base64.StdEncoding.EncodeToString([]byte(`<h2>About</h2><p>A <strong>packet</strong> library.</p>`))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/resources/1997" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":1997,"name":"ProtocolLib","tag":"Packets","likes":1234,"downloads":987654,
			"testedVersions":["1.21"],"icon":{"url":"data/icon.jpg"},"file":{"type":".jar"},
			"version":{"id":500001},"updateDate":1714521600,
			"description":"` + desc + `","sourceCodeLink":"https://github.com/dmulloy2/ProtocolLib"}`))
	}))
	defer srv.Close()

	p, err := testClient(srv.URL, "https://web.example").GetProject(context.Background(), "1997")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "1997" || p.Followers != 1234 {
		t.Fatalf("wrong project mapping: %+v", p)
	}
	// Base64 HTML description decoded and converted to Markdown.
	want := "## About\n\nA **packet** library."
	if p.Body != want {
		t.Fatalf("want body %q, got %q", want, p.Body)
	}
	if p.SourceURL == nil || *p.SourceURL != "https://github.com/dmulloy2/ProtocolLib" {
		t.Errorf("source link not mapped: %v", p.SourceURL)
	}
}

func TestGetVersionsDownloadStrategy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/resources/1997":
			w.Write([]byte(resourceJSON))
		case "/resources/1997/versions":
			if got := r.URL.Query().Get("sort"); got != "-releaseDate" {
				t.Errorf("versions must be requested newest-first, got sort=%q", got)
			}
			w.Write([]byte(`[
				{"id":500001,"name":"5.3.0","releaseDate":1714521600},
				{"id":400000,"name":"5.2.0","releaseDate":1700000000}
			]`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := testClient(srv.URL, "")
	// Loader/mcVersion are ignored: Spiget has no per-version compatibility
	// metadata, so filtering would hide every installable version.
	versions, err := c.GetVersions(context.Background(), "1997", "paper", "1.21.4")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("want 2 versions, got %d", len(versions))
	}
	latest, older := versions[0], versions[1]
	if latest.ID != "500001" || latest.VersionNumber != "5.3.0" {
		t.Fatalf("wrong version mapping: %+v", latest)
	}
	// Latest downloads through the resource endpoint, older versions through
	// the CDN proxy.
	if got := latest.Files[0].URL; got != srv.URL+"/resources/1997/download" {
		t.Errorf("latest download URL wrong: %q", got)
	}
	if got := older.Files[0].URL; got != srv.URL+"/resources/1997/versions/400000/download/proxy" {
		t.Errorf("older download URL wrong: %q", got)
	}
	if latest.Files[0].Filename != "ProtocolLib-5.3.0.jar" {
		t.Errorf("wrong filename: %q", latest.Files[0].Filename)
	}
	if latest.Files[0].Hashes.SHA256 != "" {
		t.Error("spiget has no hashes; SHA256 must stay empty")
	}
	// Bukkit-family loaders so plugin servers pass compatibility checks and
	// files land in /plugins.
	if len(latest.Loaders) != 4 || latest.Loaders[0] != "paper" {
		t.Errorf("wrong loaders: %v", latest.Loaders)
	}
	if latest.DatePublished != "2024-05-01T00:00:00Z" {
		t.Errorf("wrong date: %q", latest.DatePublished)
	}
}

func TestPremiumAndExternalDownloads(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/resources/2":
			w.Write([]byte(`{"id":2,"name":"FancyShop","premium":true,"file":{"type":".jar"},"version":{"id":9}}`))
		case "/resources/2/versions/9":
			w.Write([]byte(`{"id":9,"name":"1.0","releaseDate":1700000000}`))
		case "/resources/3":
			w.Write([]byte(`{"id":3,"name":"ExtTool","external":true,"file":{"type":"external"},"version":{"id":31}}`))
		case "/resources/3/versions":
			w.Write([]byte(`[{"id":31,"name":"2.0"},{"id":30,"name":"1.9"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := testClient(srv.URL, "")

	// Premium resources are never downloadable; the empty URL surfaces the
	// standard "does not permit third-party downloads" error upstream.
	v, err := c.GetVersion(context.Background(), "2", "9")
	if err != nil {
		t.Fatal(err)
	}
	if v.Files[0].URL != "" {
		t.Errorf("premium download URL must be empty, got %q", v.Files[0].URL)
	}

	// External resources: only the latest version can be fetched (the
	// /download redirect follows the external host); older versions were
	// never mirrored.
	versions, err := c.GetVersions(context.Background(), "3", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := versions[0].Files[0].URL; got != srv.URL+"/resources/3/download" {
		t.Errorf("external latest must use /download, got %q", got)
	}
	if versions[1].Files[0].URL != "" {
		t.Errorf("external non-latest must be empty, got %q", versions[1].Files[0].URL)
	}
	// A non-".ext" file type falls back to .jar.
	if versions[0].Files[0].Filename != "ExtTool-2.0.jar" {
		t.Errorf("wrong filename: %q", versions[0].Files[0].Filename)
	}
}

func TestGetCategoriesDedupes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/categories" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":4,"name":"Mechanics"},{"id":2,"name":"Chat"},{"id":24,"name":"Chat"}]`))
	}))
	defer srv.Close()

	cats, err := testClient(srv.URL, "").GetCategories(context.Background(), "plugin")
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) != 2 {
		t.Fatalf("duplicate names must collapse, got %+v", cats)
	}
	if cats[0].Name != "Chat" || cats[0].ID != "2" || cats[1].Name != "Mechanics" {
		t.Fatalf("wrong categories: %+v", cats)
	}

	empty, err := testClient(srv.URL, "").GetCategories(context.Background(), "mod")
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatal("non-plugin project types have no spigot categories")
	}
}
