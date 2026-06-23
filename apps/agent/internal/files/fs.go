package files

import (
	"archive/zip"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Entry struct {
	Name     string    `json:"name"`
	Type     string    `json:"type"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

type Listing struct {
	Path    string  `json:"path"`
	Entries []Entry `json:"entries"`
}

// TreeEntry is a single file discovered by a recursive walk. Path is relative to
// the walk root, slash-separated, so callers can prefix it with the root.
type TreeEntry struct {
	Path     string    `json:"path"`
	Type     string    `json:"type"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

type Tree struct {
	Path      string      `json:"path"`
	Entries   []TreeEntry `json:"entries"`
	Truncated bool        `json:"truncated"`
}

func Resolve(base, userPath string) (string, error) {
	cleanBase, err := secureBase(base)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(cleanBase, filepath.Clean("/"+userPath))
	if !withinBase(cleanBase, abs) {
		return "", fmt.Errorf("path escapes server directory")
	}
	return abs, nil
}

func ResolveExisting(base, userPath string) (string, error) {
	abs, err := Resolve(base, userPath)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	cleanBase, err := secureBase(base)
	if err != nil {
		return "", err
	}
	if !withinBase(cleanBase, resolved) {
		return "", fmt.Errorf("path escapes server directory")
	}
	return resolved, nil
}

func ResolveForWrite(base, userPath string) (string, error) {
	abs, err := Resolve(base, userPath)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(abs)
	if _, err := os.Stat(parent); err == nil {
		resolvedParent, err := filepath.EvalSymlinks(parent)
		if err != nil {
			return "", err
		}
		cleanBase, err := secureBase(base)
		if err != nil {
			return "", err
		}
		if !withinBase(cleanBase, resolvedParent) {
			return "", fmt.Errorf("path escapes server directory")
		}
	}
	return abs, nil
}

func secureBase(base string) (string, error) {
	cleanBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	cleanBase = filepath.Clean(cleanBase)
	if resolved, err := filepath.EvalSymlinks(cleanBase); err == nil {
		cleanBase = resolved
	}
	return cleanBase, nil
}

func withinBase(base, path string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absPath = filepath.Clean(absPath)
	rel, err := filepath.Rel(base, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func List(base, userPath string) (*Listing, error) {
	abs, err := ResolveExisting(base, userPath)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}

	listing := &Listing{Path: userPath, Entries: make([]Entry, 0, len(entries))}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		t := "file"
		if e.IsDir() {
			t = "dir"
		}
		listing.Entries = append(listing.Entries, Entry{
			Name:     e.Name(),
			Type:     t,
			Size:     info.Size(),
			Modified: info.ModTime(),
		})
	}
	return listing, nil
}

// ListTree recursively walks userPath and returns every file beneath it in a
// single pass — done locally on the agent so callers avoid one HTTP round-trip
// per directory. Symlinks are not followed (WalkDir uses Lstat), so the walk
// stays within the resolved root. maxDepth limits how deep directories are
// descended (<=0 means unlimited); maxEntries caps the result (<=0 means
// unlimited) and sets Truncated when hit.
func ListTree(base, userPath string, maxDepth, maxEntries int) (*Tree, error) {
	abs, err := ResolveExisting(base, userPath)
	if err != nil {
		return nil, err
	}

	tree := &Tree{Path: userPath, Entries: make([]TreeEntry, 0, 256)}
	err = filepath.WalkDir(abs, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Skip unreadable subtrees rather than aborting the whole walk.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if p == abs {
			return nil // don't emit the root itself
		}

		rel, err := filepath.Rel(abs, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		depth := strings.Count(rel, "/") + 1

		if d.IsDir() {
			if maxDepth > 0 && depth >= maxDepth {
				return filepath.SkipDir
			}
			return nil // we only emit files
		}

		if maxEntries > 0 && len(tree.Entries) >= maxEntries {
			tree.Truncated = true
			return filepath.SkipAll
		}

		var size int64
		var mod time.Time
		if info, err := d.Info(); err == nil {
			size = info.Size()
			mod = info.ModTime()
		}
		tree.Entries = append(tree.Entries, TreeEntry{
			Path:     rel,
			Type:     "file",
			Size:     size,
			Modified: mod,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tree, nil
}

func ReadContent(base, userPath string) ([]byte, error) {
	abs, err := ResolveExisting(base, userPath)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(abs)
}

func WriteContent(base, userPath string, data []byte) error {
	abs, err := ResolveForWrite(base, userPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return err
	}
	return os.WriteFile(abs, data, 0644)
}

func Delete(base, userPath string) error {
	abs, err := ResolveExisting(base, userPath)
	if err != nil {
		return err
	}
	return os.RemoveAll(abs)
}

func Rename(base, fromPath, toPath string) error {
	src, err := ResolveExisting(base, fromPath)
	if err != nil {
		return err
	}
	dst, err := ResolveForWrite(base, toPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

func Mkdir(base, userPath string) error {
	abs, err := ResolveForWrite(base, userPath)
	if err != nil {
		return err
	}
	return os.MkdirAll(abs, 0755)
}

func WriteUpload(base, dirPath, filename string, src io.Reader) error {
	dir, err := ResolveForWrite(base, dirPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	dst := filepath.Join(dir, filepath.Base(filename))
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, src)
	return err
}

// FileFingerprints returns the identifiers used to recognize a jar against
// upstream file indexes in a single read: the lowercase hex sha512 (Modrinth
// keys files by sha1/sha512) and the CurseForge "fingerprint" (a MurmurHash2 of
// the file with whitespace bytes stripped, seed 1). The file is read once and
// fed to both; the murmur input must be buffered because MurmurHash2 needs the
// stripped length up front.
func FileFingerprints(base, userPath string) (sha512hex string, murmur2 uint32, err error) {
	abs, err := ResolveExisting(base, userPath)
	if err != nil {
		return "", 0, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()

	h := sha512.New()
	stripped := make([]byte, 0, 1<<20)
	buf := make([]byte, 64*1024)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			h.Write(chunk)
			for _, b := range chunk {
				// CurseForge strips tab/LF/CR/space before fingerprinting.
				if b == 9 || b == 10 || b == 13 || b == 32 {
					continue
				}
				stripped = append(stripped, b)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", 0, rerr
		}
	}
	return hex.EncodeToString(h.Sum(nil)), murmurHash2(stripped, 1), nil
}

// murmurHash2 is Austin Appleby's 32-bit MurmurHash2, the variant CurseForge
// uses for file fingerprints (seed 1, over the whitespace-stripped bytes).
func murmurHash2(data []byte, seed uint32) uint32 {
	const m = 0x5bd1e995
	const r = 24
	length := len(data)
	h := seed ^ uint32(length)

	nblocks := length / 4
	for i := 0; i < nblocks; i++ {
		j := i * 4
		k := uint32(data[j]) | uint32(data[j+1])<<8 | uint32(data[j+2])<<16 | uint32(data[j+3])<<24
		k *= m
		k ^= k >> r
		k *= m
		h *= m
		h ^= k
	}

	tail := data[nblocks*4:]
	switch len(tail) {
	case 3:
		h ^= uint32(tail[2]) << 16
		fallthrough
	case 2:
		h ^= uint32(tail[1]) << 8
		fallthrough
	case 1:
		h ^= uint32(tail[0])
		h *= m
	}

	h ^= h >> 13
	h *= m
	h ^= h >> 15
	return h
}

func IsDir(base, userPath string) (bool, error) {
	abs, err := ResolveExisting(base, userPath)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

func ZipDir(base, userPath string, w io.Writer) error {
	abs, err := ResolveExisting(base, userPath)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	defer zw.Close()

	return filepath.Walk(abs, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		f, err := zw.Create(rel)
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(f, src)
		return err
	})
}
