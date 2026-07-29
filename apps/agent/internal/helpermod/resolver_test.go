package helpermod

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

var embeddedJar = []byte("embedded-build-bytes")

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func testFallback() Fallback {
	return Fallback{
		Data:              embeddedJar,
		ModVersion:        "1.0.0",
		MinecraftVersions: []string{"26.2"},
		ProtocolVersion:   1,
	}
}

// indexServer serves an index plus jars over TLS, mimicking the published
// release layout. Returns the server and a counter of jar downloads so tests can
// assert the disk cache is actually used.
func indexServer(t *testing.T, builds func(baseURL string) Index, jars map[string][]byte) (*httptest.Server, *int32) {
	t.Helper()

	var downloads int32
	mux := http.NewServeMux()
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/index.json", func(w http.ResponseWriter, r *http.Request) {
		body, err := json.Marshal(builds(srv.URL))
		if err != nil {
			t.Errorf("marshal index: %v", err)
			return
		}
		_, _ = w.Write(body)
	})

	for name, data := range jars {
		payload := data
		mux.HandleFunc("/jars/"+name, func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&downloads, 1)
			_, _ = w.Write(payload)
		})
	}

	return srv, &downloads
}

func newResolver(t *testing.T, srv *httptest.Server, indexPath string) *Resolver {
	t.Helper()
	r := &Resolver{
		CacheDir:    t.TempDir(),
		MaxProtocol: 1,
		Fallback:    testFallback(),
	}
	if srv != nil {
		r.IndexURL = srv.URL + indexPath
		r.HTTPClient = srv.Client()
	}
	return r
}

func TestResolvePrefersPublishedBuild(t *testing.T) {
	remote := []byte("published-26.3-build")
	srv, downloads := indexServer(t,
		func(base string) Index {
			return Index{
				SchemaVersion: 1,
				Builds: []Build{{
					ModVersion:        "1.2.0",
					Loader:            "fabric",
					MinecraftVersions: []string{"26.3"},
					ProtocolVersion:   1,
					URL:               base + "/jars/helper-26.3.jar",
					SHA256:            digest(remote),
					Size:              int64(len(remote)),
				}},
			}
		},
		map[string][]byte{"helper-26.3.jar": remote},
	)

	r := newResolver(t, srv, "/index.json")

	got, err := r.Resolve(context.Background(), "fabric", "26.3")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got == nil {
		t.Fatal("expected a build for 26.3, got none")
	}
	if string(got.Data) != string(remote) {
		t.Error("resolved the wrong jar")
	}
	if got.Source != SourceDownload {
		t.Errorf("source = %s, want %s", got.Source, SourceDownload)
	}
	if got.ModVersion != "1.2.0" {
		t.Errorf("mod version = %s, want 1.2.0", got.ModVersion)
	}
	if *downloads != 1 {
		t.Errorf("downloaded %d times, want 1", *downloads)
	}
}

// The whole point of the design: a version the shipped agent knows nothing about
// becomes supported by publishing a build, with no manager release.
func TestResolveFindsVersionNewerThanEmbedded(t *testing.T) {
	remote := []byte("future-build")
	srv, _ := indexServer(t,
		func(base string) Index {
			return Index{SchemaVersion: 1, Builds: []Build{{
				ModVersion:        "2.0.0",
				Loader:            "fabric",
				MinecraftVersions: []string{"27.1"},
				ProtocolVersion:   1,
				URL:               base + "/jars/f.jar",
				SHA256:            digest(remote),
				Size:              int64(len(remote)),
			}}}
		},
		map[string][]byte{"f.jar": remote},
	)

	r := newResolver(t, srv, "/index.json")

	got, err := r.Resolve(context.Background(), "fabric", "27.1")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got == nil || string(got.Data) != string(remote) {
		t.Fatal("a published build for an unknown-to-this-agent version was not used")
	}
}

func TestResolveUsesDiskCacheOnSecondCall(t *testing.T) {
	remote := []byte("cached-build")
	srv, downloads := indexServer(t,
		func(base string) Index {
			return Index{SchemaVersion: 1, Builds: []Build{{
				ModVersion:        "1.2.0",
				Loader:            "fabric",
				MinecraftVersions: []string{"26.3"},
				ProtocolVersion:   1,
				URL:               base + "/jars/c.jar",
				SHA256:            digest(remote),
				Size:              int64(len(remote)),
			}}}
		},
		map[string][]byte{"c.jar": remote},
	)

	r := newResolver(t, srv, "/index.json")
	cacheDir := r.CacheDir

	if _, err := r.Resolve(context.Background(), "fabric", "26.3"); err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// A fresh resolver sharing the cache dir models an agent restart.
	r2 := &Resolver{
		IndexURL:    r.IndexURL,
		CacheDir:    cacheDir,
		MaxProtocol: 1,
		Fallback:    testFallback(),
		HTTPClient:  srv.Client(),
	}
	got, err := r2.Resolve(context.Background(), "fabric", "26.3")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if got.Source != SourceCache {
		t.Errorf("source = %s, want %s", got.Source, SourceCache)
	}
	if *downloads != 1 {
		t.Errorf("jar downloaded %d times, want 1 — the cache is not being used", *downloads)
	}
}

// A tampered or corrupted jar must never be installed, and must not poison the
// cache for later runs.
func TestResolveRejectsDigestMismatch(t *testing.T) {
	served := []byte("not-what-the-index-claims")
	srv, _ := indexServer(t,
		func(base string) Index {
			return Index{SchemaVersion: 1, Builds: []Build{{
				ModVersion:        "1.2.0",
				Loader:            "fabric",
				MinecraftVersions: []string{"26.3"},
				ProtocolVersion:   1,
				URL:               base + "/jars/bad.jar",
				SHA256:            digest([]byte("something-else")),
				Size:              int64(len(served)),
			}}}
		},
		map[string][]byte{"bad.jar": served},
	)

	r := newResolver(t, srv, "/index.json")

	got, err := r.Resolve(context.Background(), "fabric", "26.3")
	if got != nil {
		t.Error("installed a jar whose digest did not match the index")
	}
	if err == nil {
		t.Error("expected an error for a digest mismatch")
	}

	entries, _ := os.ReadDir(r.CacheDir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".jar" {
			t.Errorf("unverified jar %s was written to the cache", e.Name())
		}
	}
}

// Decoupled release cadences reintroduce skew: a build speaking a newer link
// protocol than this agent understands must be skipped, not installed.
func TestResolveSkipsNewerProtocol(t *testing.T) {
	remote := []byte("protocol-2-build")
	srv, _ := indexServer(t,
		func(base string) Index {
			return Index{SchemaVersion: 1, Builds: []Build{{
				ModVersion:        "3.0.0",
				Loader:            "fabric",
				MinecraftVersions: []string{"26.2"},
				ProtocolVersion:   2,
				URL:               base + "/jars/p2.jar",
				SHA256:            digest(remote),
				Size:              int64(len(remote)),
			}}}
		},
		map[string][]byte{"p2.jar": remote},
	)

	r := newResolver(t, srv, "/index.json")

	got, err := r.Resolve(context.Background(), "fabric", "26.2")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got == nil {
		t.Fatal("expected the embedded build as a fallback")
	}
	if got.Source != SourceEmbedded {
		t.Errorf("source = %s, want %s — a protocol-2 build must not be installed by a protocol-1 agent",
			got.Source, SourceEmbedded)
	}
}

func TestResolveFallsBackWhenIndexUnreachable(t *testing.T) {
	r := &Resolver{
		IndexURL:    "https://127.0.0.1:1/index.json", // nothing listening
		CacheDir:    t.TempDir(),
		MaxProtocol: 1,
		Fallback:    testFallback(),
		HTTPClient:  &http.Client{Timeout: 2 * time.Second},
	}

	got, err := r.Resolve(context.Background(), "fabric", "26.2")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got == nil || got.Source != SourceEmbedded {
		t.Fatal("an unreachable index must fall back to the embedded build")
	}
}

// An offline host that previously saw an index should keep installing what it
// last knew about rather than regressing to the embedded build.
func TestResolveUsesStaleIndexWhenOffline(t *testing.T) {
	remote := []byte("previously-seen-build")
	cacheDir := t.TempDir()

	idx := Index{SchemaVersion: 1, Builds: []Build{{
		ModVersion:        "1.2.0",
		Loader:            "fabric",
		MinecraftVersions: []string{"26.3"},
		ProtocolVersion:   1,
		URL:               "https://example.invalid/jars/x.jar",
		SHA256:            digest(remote),
		Size:              int64(len(remote)),
	}}}
	body, _ := json.Marshal(idx)
	if err := os.WriteFile(filepath.Join(cacheDir, indexCacheName), body, 0o644); err != nil {
		t.Fatal(err)
	}
	// The jar is already cached from that earlier successful run.
	if err := os.WriteFile(filepath.Join(cacheDir, jarCacheName(digest(remote))), remote, 0o644); err != nil {
		t.Fatal(err)
	}

	r := &Resolver{
		IndexURL:    "https://127.0.0.1:1/index.json",
		CacheDir:    cacheDir,
		MaxProtocol: 1,
		Fallback:    testFallback(),
		HTTPClient:  &http.Client{Timeout: 2 * time.Second},
	}

	got, err := r.Resolve(context.Background(), "fabric", "26.3")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got == nil || got.Source != SourceCache {
		t.Fatalf("offline resolve did not use the cached index + jar, got %+v", got)
	}
}

func TestResolveReturnsNothingForUnknownVersion(t *testing.T) {
	r := newResolver(t, nil, "")

	got, err := r.Resolve(context.Background(), "fabric", "1.21.11")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != nil {
		t.Error("returned a build for a version nothing supports")
	}
}

func TestResolveWithoutIndexURLUsesEmbedded(t *testing.T) {
	r := newResolver(t, nil, "")

	got, err := r.Resolve(context.Background(), "fabric", "26.2")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got == nil || got.Source != SourceEmbedded {
		t.Fatal("with lookups disabled the embedded build must still be served")
	}
}

func TestIndexRejectsUnknownSchema(t *testing.T) {
	_, err := ParseIndex([]byte(`{"schema_version": 99, "builds": []}`))
	if err == nil {
		t.Error("an index from a newer format must be rejected, not guessed at")
	}
}

func TestSelectRejectsUnsafeEntries(t *testing.T) {
	good := digest([]byte("x"))
	cases := map[string]Build{
		"plaintext url": {Loader: "fabric", MinecraftVersions: []string{"26.2"}, ProtocolVersion: 1,
			URL: "http://example.com/a.jar", SHA256: good, Size: 10},
		"missing digest": {Loader: "fabric", MinecraftVersions: []string{"26.2"}, ProtocolVersion: 1,
			URL: "https://example.com/a.jar", SHA256: "", Size: 10},
		"absurd size": {Loader: "fabric", MinecraftVersions: []string{"26.2"}, ProtocolVersion: 1,
			URL: "https://example.com/a.jar", SHA256: good, Size: 1 << 40},
		"wrong loader": {Loader: "forge", MinecraftVersions: []string{"26.2"}, ProtocolVersion: 1,
			URL: "https://example.com/a.jar", SHA256: good, Size: 10},
	}

	for name, b := range cases {
		idx := &Index{SchemaVersion: 1, Builds: []Build{b}}
		if _, err := idx.Select("fabric", "26.2", 1); err == nil {
			t.Errorf("%s: entry should have been rejected", name)
		}
	}
}

func TestSelectPrefersHighestUsableProtocol(t *testing.T) {
	good := digest([]byte("x"))
	mk := func(mod string, proto int) Build {
		return Build{
			ModVersion: mod, Loader: "fabric", MinecraftVersions: []string{"26.2"},
			ProtocolVersion: proto, URL: "https://example.com/" + mod + ".jar",
			SHA256: good, Size: 10,
		}
	}
	idx := &Index{SchemaVersion: 1, Builds: []Build{mk("old", 1), mk("new", 2), mk("mid", 2)}}

	got, err := idx.Select("fabric", "26.2", 2)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if got.ProtocolVersion != 2 {
		t.Errorf("picked protocol %d, want the highest this agent speaks (2)", got.ProtocolVersion)
	}

	// Same index, an agent that only speaks v1.
	got, err = idx.Select("fabric", "26.2", 1)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if got.ModVersion != "old" {
		t.Errorf("picked %s, want the protocol-1 build", got.ModVersion)
	}
}

func TestFetchRejectsOversizedBody(t *testing.T) {
	big := make([]byte, 2048)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(big)
	}))
	t.Cleanup(srv.Close)

	r := &Resolver{HTTPClient: srv.Client()}
	if _, err := r.fetch(context.Background(), srv.URL, 100); err == nil {
		t.Error("a body larger than declared must be rejected")
	}
}

func TestFetchRejectsPlaintext(t *testing.T) {
	r := &Resolver{}
	if _, err := r.fetch(context.Background(), "http://example.com/x.json", 100); err == nil {
		t.Error("plaintext fetches must be refused")
	}
}

func ExampleIndex_Select() {
	idx := &Index{SchemaVersion: 1, Builds: []Build{{
		ModVersion: "1.2.0", Loader: "fabric", MinecraftVersions: []string{"26.3"},
		ProtocolVersion: 1, URL: "https://example.com/a.jar",
		SHA256: digest([]byte("a")), Size: 1,
	}}}

	b, err := idx.Select("fabric", "26.3", 1)
	if err != nil {
		return
	}
	fmt.Println(b.ModVersion)
	// Output: 1.2.0
}
