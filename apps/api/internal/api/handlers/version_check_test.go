package handlers

import (
	"context"
	"errors"
	"testing"

	"github.com/mcsm/api/internal/mods/modrinth"
)

// fakeSource implements sourceClient for classifyForTarget tests. Only
// GetVersions is exercised; the rest satisfy the interface.
type fakeSource struct {
	versions []modrinth.Version
	err      error
}

func (f *fakeSource) Search(context.Context, modrinth.SearchParams) (*modrinth.SearchResult, error) {
	return nil, nil
}
func (f *fakeSource) GetProject(context.Context, string) (*modrinth.Project, error) {
	return nil, nil
}
func (f *fakeSource) GetVersions(context.Context, string, string, string) ([]modrinth.Version, error) {
	return f.versions, f.err
}

func ver(id, num, fileURL string) modrinth.Version {
	v := modrinth.Version{ID: id, VersionNumber: num}
	if fileURL != "" {
		f := modrinth.VersionFile{URL: fileURL, Filename: num + ".jar", Primary: true}
		v.Files = []modrinth.VersionFile{f}
	}
	return v
}

func TestClassifyForTarget(t *testing.T) {
	tests := []struct {
		name       string
		src        *fakeSource
		current    string
		wantStatus string
		wantTarget string
	}{
		{
			name:       "no builds for target is incompatible",
			src:        &fakeSource{versions: nil},
			current:    "v-old",
			wantStatus: compatIncompatible,
		},
		{
			name:       "current build already supports target",
			src:        &fakeSource{versions: []modrinth.Version{ver("v-new", "2.0", "http://x"), ver("v-cur", "1.0", "http://x")}},
			current:    "v-cur",
			wantStatus: compatSupported,
		},
		{
			name:       "newer downloadable build is a compatible move",
			src:        &fakeSource{versions: []modrinth.Version{ver("v-new", "2.0", "http://x"), ver("v-mid", "1.5", "http://x")}},
			current:    "v-cur",
			wantStatus: compatCompatible,
			wantTarget: "v-new",
		},
		{
			name:       "builds exist but none downloadable is incompatible",
			src:        &fakeSource{versions: []modrinth.Version{ver("v-new", "2.0", "")}},
			current:    "v-cur",
			wantStatus: compatIncompatible,
		},
		{
			name:       "upstream error is unknown",
			src:        &fakeSource{err: errors.New("boom")},
			current:    "v-cur",
			wantStatus: compatUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := modCompat{}
			got := classifyForTarget(context.Background(), tc.src, "proj", tc.current, "fabric", "1.21", &out)
			if got != tc.wantStatus {
				t.Fatalf("status = %q, want %q", got, tc.wantStatus)
			}
			if tc.wantTarget != "" && out.TargetVersionID != tc.wantTarget {
				t.Fatalf("target version id = %q, want %q", out.TargetVersionID, tc.wantTarget)
			}
		})
	}
}
