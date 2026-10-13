package helpermod

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func signedBuild(remote []byte, base string) Index {
	return Index{SchemaVersion: 1, Builds: []Build{{
		ModVersion: "2.0.0", Loader: "fabric", MinecraftVersions: []string{"26.3"},
		ProtocolVersion: 1, URL: base + "/jars/s.jar", SHA256: digest(remote), Size: int64(len(remote)),
	}}}
}

// An index signed by a key the agent does not trust chooses nothing.
func TestResolveIgnoresIndexSignedByAnotherKey(t *testing.T) {
	remote := []byte("published-by-someone-else")
	srv, downloads := indexServer(t, func(base string) Index { return signedBuild(remote, base) },
		map[string][]byte{"s.jar": remote})
	r := newResolver(t, srv, "/index.json")
	other, _, _ := ed25519.GenerateKey(nil)
	r.PublicKeys = []ed25519.PublicKey{other}

	got, err := r.Resolve(context.Background(), "fabric", "26.3")
	if got != nil && got.Source != SourceEmbedded {
		t.Fatalf("an index with an untrusted signature installed %+v", got)
	}
	if err == nil && got == nil {
		t.Fatal("an unverifiable index must read as unavailable, not as an authoritative empty answer")
	}
	if *downloads != 0 {
		t.Fatal("a jar named by an untrusted index was downloaded")
	}
}

// The cache directory is writable by the agent's user; a rewritten cached
// index must not be believed.
func TestResolveRejectsRewrittenCachedIndex(t *testing.T) {
	remote := []byte("planted")
	cacheDir := t.TempDir()
	body, _ := json.Marshal(signedBuild(remote, "https://example.invalid"))
	_ = os.WriteFile(filepath.Join(cacheDir, indexCacheName), body, 0o644)
	_ = os.WriteFile(filepath.Join(cacheDir, indexCacheName+SignatureSuffix), []byte("bm90LWEtc2lnbmF0dXJl\n"), 0o644)
	_ = os.WriteFile(filepath.Join(cacheDir, jarCacheName(digest(remote))), remote, 0o644)

	r := &Resolver{
		IndexURL: "https://127.0.0.1:1/index.json", CacheDir: cacheDir, MaxProtocol: 1,
		Fallback: testFallback(), HTTPClient: &http.Client{Timeout: 2 * time.Second},
		PublicKeys: []ed25519.PublicKey{testPub},
	}
	got, _ := r.Resolve(context.Background(), "fabric", "26.3")
	if got != nil {
		t.Fatalf("a rewritten cached index installed %+v", got)
	}
}

// An operator may accept an unsigned index explicitly.
func TestResolveAllowUnsigned(t *testing.T) {
	remote := []byte("self-hosted")
	mux := http.NewServeMux()
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) {
		body, _ := json.Marshal(signedBuild(remote, srv.URL))
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/jars/s.jar", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(remote) })

	r := newResolver(t, srv, "/index.json")
	r.PublicKeys = nil
	if got, _ := r.Resolve(context.Background(), "fabric", "26.3"); got != nil && got.Source == SourceDownload {
		t.Fatal("an unsigned index was used without the opt-in")
	}
	r.AllowUnsigned = true
	got, err := r.Resolve(context.Background(), "fabric", "26.3")
	if err != nil || got == nil || got.Source != SourceDownload {
		t.Fatalf("with AllowUnsigned the index should be used: %+v, %v", got, err)
	}
}

func TestParsePublicKeys(t *testing.T) {
	if _, err := ParsePublicKeys("not-base64!"); err == nil {
		t.Fatal("garbage accepted as a key")
	}
	keys, err := ParsePublicKeys(" , ")
	if err != nil || len(keys) != 0 {
		t.Fatalf("empty list = %v, %v", keys, err)
	}
}
