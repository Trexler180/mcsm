package modrinth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestBuildFacets(t *testing.T) {
	tests := []struct {
		name string
		p    SearchParams
		want []string // substrings that must all be present
		omit []string // substrings that must NOT be present
	}{
		{
			name: "default mod",
			p:    SearchParams{},
			want: []string{`["project_type:mod"]`, `"server_side:optional"`},
		},
		{
			name: "loader and version combine (A1 regression)",
			p:    SearchParams{Loader: "fabric", MCVersion: "1.21.4"},
			want: []string{`["categories:fabric"]`, `["versions:1.21.4"]`, `["project_type:mod"]`},
		},
		{
			name: "plugin type drops loader-less, keeps server_side",
			p:    SearchParams{ProjectType: "plugin", Loader: "paper"},
			want: []string{`["project_type:plugin"]`, `["categories:paper"]`, `"server_side:required"`},
		},
		{
			name: "resourcepack omits server_side facet",
			p:    SearchParams{ProjectType: "resourcepack"},
			want: []string{`["project_type:resourcepack"]`},
			omit: []string{"server_side"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.p.buildFacets()
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("facets %q missing %q", got, w)
				}
			}
			for _, o := range tt.omit {
				if strings.Contains(got, o) {
					t.Errorf("facets %q should not contain %q", got, o)
				}
			}
		})
	}
}

func TestLoaderForPlatform(t *testing.T) {
	cases := map[string]string{
		"fabric": "fabric", "Fabric": "fabric",
		"neoforge": "neoforge", "paper": "paper",
		"spigot": "spigot", "vanilla": "", "unknown": "",
	}
	for in, want := range cases {
		if got := LoaderForPlatform(in); got != want {
			t.Errorf("LoaderForPlatform(%q)=%q want %q", in, got, want)
		}
	}
}

func TestIsPluginPlatform(t *testing.T) {
	if !IsPluginPlatform("paper") || !IsPluginPlatform("purpur") {
		t.Error("paper/purpur should be plugin platforms")
	}
	if IsPluginPlatform("fabric") || IsPluginPlatform("vanilla") {
		t.Error("fabric/vanilla are not plugin platforms")
	}
}

func TestDownloadVerifiesSHA(t *testing.T) {
	// httptest binds loopback, which the SSRF dial guard blocks; relax it for the
	// duration of this test.
	allowLoopbackDownloads = true
	defer func() { allowLoopbackDownloads = false }()

	payload := []byte("fake-jar-bytes")
	sum := sha256.Sum256(payload)
	good := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	c := New()

	// Correct hash → temp file with matching content.
	path, err := c.Download(context.Background(), srv.URL, good)
	if err != nil {
		t.Fatalf("download with good sha failed: %v", err)
	}
	defer os.Remove(path)
	data, _ := os.ReadFile(path)
	if string(data) != string(payload) {
		t.Errorf("downloaded content mismatch")
	}

	// Wrong hash → error, no leftover file.
	bad, err := c.Download(context.Background(), srv.URL, "deadbeef")
	if err == nil {
		t.Error("expected sha mismatch error")
	}
	if bad != "" {
		os.Remove(bad)
		t.Error("expected empty path on sha mismatch")
	}
}
