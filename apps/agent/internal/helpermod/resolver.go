package helpermod

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Source describes where a resolved jar came from, for logging and for the
// console note the user sees.
type Source string

const (
	SourceEmbedded Source = "embedded"
	SourceCache    Source = "cache"
	SourceDownload Source = "download"
)

// Resolved is a jar ready to install.
type Resolved struct {
	Data       []byte
	Source     Source
	ModVersion string
	SHA256     string
}

// Fallback supplies the build compiled into the agent binary.
type Fallback struct {
	Data              []byte
	ModVersion        string
	MinecraftVersions []string
	ProtocolVersion   int
}

func (f Fallback) supports(mcVersion string) bool {
	for _, v := range f.MinecraftVersions {
		if strings.EqualFold(strings.TrimSpace(v), mcVersion) {
			return true
		}
	}
	return false
}

// Resolver finds the right helper mod build for a server.
//
// It is safe for concurrent use; several servers may start at once.
type Resolver struct {
	// IndexURL is the published index. Empty disables all network lookups, and
	// the resolver serves only the embedded build.
	IndexURL string
	// CacheDir holds downloaded jars and the last index seen.
	CacheDir string
	// TTL is how long a cached index is used before refetching.
	TTL time.Duration
	// MaxProtocol is the newest link protocol this agent speaks.
	MaxProtocol int
	// Fallback is the embedded build.
	Fallback Fallback

	HTTPClient *http.Client
	// Now is injectable for tests.
	Now func() time.Time

	mu           sync.Mutex
	index        *Index
	indexFetched time.Time
}

const defaultTTL = 6 * time.Hour

func (r *Resolver) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Resolver) client() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	// Bounded: resolving a mod must never be what stops a server from starting.
	return &http.Client{Timeout: 30 * time.Second}
}

func (r *Resolver) ttl() time.Duration {
	if r.TTL > 0 {
		return r.TTL
	}
	return defaultTTL
}

// Resolve returns the jar to install for a server.
//
// The order is deliberate: a published build that matches the server's exact
// Minecraft version beats the embedded one, because the embedded build ages the
// moment a new game version ships. When the network is unavailable the embedded
// build is used if it fits, so an offline host degrades to "the version we
// shipped with" rather than to nothing.
//
// Returns nil (no error) when nothing compatible exists. That is a normal
// outcome — a server on a version nobody has built for yet — not a failure.
func (r *Resolver) Resolve(ctx context.Context, loader, mcVersion string) (*Resolved, error) {
	mcVersion = strings.TrimSpace(mcVersion)
	if mcVersion == "" {
		return nil, nil
	}

	idx, consulted := r.loadIndex(ctx)

	if idx != nil {
		if build, err := idx.Select(loader, mcVersion, r.MaxProtocol); err == nil {
			resolved, fetchErr := r.fetchJar(ctx, build)
			if fetchErr == nil {
				return resolved, nil
			}
			// Fall through to the embedded build: a download failure should cost
			// the newest features, not the whole feature.
			if !r.fallbackUsable(mcVersion) {
				return nil, fetchErr
			}
		}
	}

	if r.fallbackUsable(mcVersion) {
		sum := sha256.Sum256(r.Fallback.Data)
		return &Resolved{
			Data:       r.Fallback.Data,
			Source:     SourceEmbedded,
			ModVersion: r.Fallback.ModVersion,
			SHA256:     hex.EncodeToString(sum[:]),
		}, nil
	}

	if !consulted {
		// We could not read the index and have no embedded build for this
		// version, so we genuinely do not know whether one exists.
		//
		// This must not be reported as "nothing supports this version": the
		// caller reacts to that by deleting the installed jar, and doing so
		// because of a network blip would remove a mod that was working fine.
		return nil, ErrIndexUnavailable
	}

	// The index was readable and simply has nothing for this version. That is a
	// real answer, and the caller may act on it.
	return nil, nil
}

// ErrIndexUnavailable means the published index could not be consulted, so the
// absence of a build is unproven.
var ErrIndexUnavailable = errors.New("helpermod: could not consult the build index")

func (r *Resolver) fallbackUsable(mcVersion string) bool {
	return len(r.Fallback.Data) > 0 &&
		r.Fallback.supports(mcVersion) &&
		r.Fallback.ProtocolVersion <= r.MaxProtocol
}

// loadIndex returns the index and whether it could be consulted at all.
//
// consulted is false only when a lookup was configured and every route to an
// index failed. With no IndexURL set the resolver is embedded-only by design, so
// consulted is true — "no build" is then an authoritative answer, not ignorance.
func (r *Resolver) loadIndex(ctx context.Context) (idx *Index, consulted bool) {
	if r.IndexURL == "" {
		return nil, true
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.index != nil && r.now().Sub(r.indexFetched) < r.ttl() {
		return r.index, true
	}

	if data, err := r.fetch(ctx, r.IndexURL, maxIndexBytes); err == nil {
		if parsed, err := ParseIndex(data); err == nil {
			r.index = parsed
			r.indexFetched = r.now()
			r.writeCache(indexCacheName, data)
			return parsed, true
		}
	}

	// Network failed or served something unusable. A previously cached index is
	// still better information than none — a host that has been offline for a
	// week should keep installing the build it last knew about.
	if data, err := r.readCache(indexCacheName); err == nil {
		if parsed, err := ParseIndex(data); err == nil {
			r.index = parsed
			// Deliberately not stamping indexFetched: this is stale data, so the
			// next resolve should try the network again.
			return parsed, true
		}
	}

	if r.index != nil {
		return r.index, true
	}
	return nil, false
}

const (
	indexCacheName = "index.json"
	maxIndexBytes  = 1 << 20
)

// fetchJar returns the build's jar, from disk cache when possible.
func (r *Resolver) fetchJar(ctx context.Context, b *Build) (*Resolved, error) {
	name := jarCacheName(b.SHA256)

	if data, err := r.readCache(name); err == nil && verifyDigest(data, b.SHA256) {
		return &Resolved{Data: data, Source: SourceCache, ModVersion: b.ModVersion, SHA256: b.SHA256}, nil
	}

	data, err := r.fetch(ctx, b.URL, b.Size)
	if err != nil {
		return nil, err
	}
	if !verifyDigest(data, b.SHA256) {
		// Never cache or install unverified bytes.
		return nil, fmt.Errorf("helpermod: digest mismatch for %s", b.URL)
	}

	r.writeCache(name, data)
	return &Resolved{Data: data, Source: SourceDownload, ModVersion: b.ModVersion, SHA256: b.SHA256}, nil
}

func jarCacheName(sum string) string {
	if len(sum) > 16 {
		sum = sum[:16]
	}
	return "mcsm-helper-" + sum + ".jar"
}

func verifyDigest(data []byte, want string) bool {
	sum := sha256.Sum256(data)
	return strings.EqualFold(hex.EncodeToString(sum[:]), want)
}

// fetch performs a bounded GET. limit caps how many bytes are read, so a
// mis-stated size or a hostile server cannot exhaust memory.
func (r *Resolver) fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	if !strings.HasPrefix(strings.ToLower(url), "https://") {
		return nil, fmt.Errorf("helpermod: refusing non-https url")
	}
	if limit <= 0 || limit > maxJarBytes {
		limit = maxJarBytes
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "mcsm-agent")

	resp, err := r.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("helpermod: %s returned %d", url, resp.StatusCode)
	}

	// limit+1 so a body larger than declared is detected rather than truncated
	// into something that would fail the digest check for a confusing reason.
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("helpermod: %s exceeded its declared size", url)
	}
	return data, nil
}

func (r *Resolver) readCache(name string) ([]byte, error) {
	if r.CacheDir == "" {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(filepath.Join(r.CacheDir, name))
}

// writeCache stores a file, ignoring failures: the cache is an optimisation and
// a read-only or full disk must not break resolution.
func (r *Resolver) writeCache(name string, data []byte) {
	if r.CacheDir == "" {
		return
	}
	if err := os.MkdirAll(r.CacheDir, 0o755); err != nil {
		return
	}
	path := filepath.Join(r.CacheDir, name)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
	}
}
