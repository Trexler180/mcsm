package files

import (
	"archive/zip"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
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
	cleanBase, err := secureBase(base)
	if err != nil {
		return "", err
	}
	// The server root resolves to the base itself (a write "into /", e.g. an
	// upload landing in the server root). Its parent is outside the sandbox by
	// definition, so walking up to it would reject a perfectly legal target —
	// the base is already symlink-resolved and trusted by secureBase.
	if abs == cleanBase {
		return abs, nil
	}
	// Resolve the nearest existing ancestor, including the target itself when it
	// already exists. Checking only the immediate parent misses paths such as
	// link/new/file when link is a symlink and new does not exist yet; checking
	// only the parent also misses an existing final-component symlink.
	ancestor := abs
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			resolved, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return "", err
			}
			if !withinBase(cleanBase, resolved) {
				return "", fmt.Errorf("path escapes server directory")
			}
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		next := filepath.Dir(ancestor)
		if next == ancestor {
			return "", fmt.Errorf("path has no existing ancestor")
		}
		ancestor = next
	}
	return abs, nil
}

// openRootPath returns an os.Root plus a root-relative name for userPath.
// os.Root performs the actual filesystem operation beneath an anchored handle,
// closing the symlink-swap race that a resolve-then-open sequence would leave.
func openRootPath(base, userPath string) (*os.Root, string, error) {
	cleanBase, rel, err := rootRelativePath(base, userPath)
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(cleanBase)
	if err != nil {
		return nil, "", err
	}
	return root, rel, nil
}

func rootRelativePath(base, userPath string) (string, string, error) {
	cleanBase, err := secureBase(base)
	if err != nil {
		return "", "", err
	}
	abs, err := Resolve(cleanBase, userPath)
	if err != nil {
		return "", "", err
	}
	rel, err := filepath.Rel(cleanBase, abs)
	if err != nil || !withinBase(cleanBase, abs) {
		return "", "", fmt.Errorf("path escapes server directory")
	}
	return cleanBase, rel, nil
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
	root, name, err := openRootPath(base, userPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	dir, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
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
	root, start, err := openRootPath(base, userPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	start = filepath.ToSlash(start)

	tree := &Tree{Path: userPath, Entries: make([]TreeEntry, 0, 256)}
	err = fs.WalkDir(root.FS(), start, func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Skip unreadable subtrees rather than aborting the whole walk.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if name == start {
			return nil // don't emit the root itself
		}

		rel := rootWalkRelative(start, name)
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
	root, name, err := openRootPath(base, userPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile(name)
}

// OpenFile opens a regular file through an anchored server root. The returned
// handle remains valid after the root handle is closed by this function.
func OpenFile(base, userPath string) (*os.File, os.FileInfo, error) {
	root, name, err := openRootPath(base, userPath)
	if err != nil {
		return nil, nil, err
	}
	f, err := root.Open(name)
	root.Close()
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, fmt.Errorf("path is not a regular file")
	}
	return f, info, nil
}

func WriteContent(base, userPath string, data []byte) error {
	root, name, err := openRootPath(base, userPath)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return err
	}
	return root.WriteFile(name, data, 0644)
}

func Delete(base, userPath string) error {
	root, name, err := openRootPath(base, userPath)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.RemoveAll(name)
}

func Rename(base, fromPath, toPath string) error {
	root, src, err := openRootPath(base, fromPath)
	if err != nil {
		return err
	}
	defer root.Close()
	_, dst, err := rootRelativePath(base, toPath)
	if err != nil {
		return err
	}
	if err := root.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return root.Rename(src, dst)
}

func Mkdir(base, userPath string) error {
	root, name, err := openRootPath(base, userPath)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.MkdirAll(name, 0755)
}

func WriteUpload(base, dirPath, filename string, src io.Reader) error {
	root, dir, err := openRootPath(base, dirPath)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(dir, 0755); err != nil {
		return err
	}
	dst := filepath.Join(dir, filepath.Base(filename))
	f, err := root.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
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
	root, name, err := openRootPath(base, userPath)
	if err != nil {
		return "", 0, err
	}
	defer root.Close()
	f, err := root.Open(name)
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
	root, name, err := openRootPath(base, userPath)
	if err != nil {
		return false, err
	}
	defer root.Close()
	info, err := root.Stat(name)
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

func ZipDir(base, userPath string, w io.Writer) error {
	root, start, err := openRootPath(base, userPath)
	if err != nil {
		return err
	}
	defer root.Close()
	start = filepath.ToSlash(start)
	zw := zip.NewWriter(w)
	defer zw.Close()

	return fs.WalkDir(root.FS(), start, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel := rootWalkRelative(start, name)

		f, err := zw.Create(rel)
		if err != nil {
			return err
		}
		src, err := root.Open(name)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(f, src)
		closeErr := src.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func rootWalkRelative(start, name string) string {
	if start == "." {
		return strings.TrimPrefix(name, "./")
	}
	return strings.TrimPrefix(name, strings.TrimSuffix(start, "/")+"/")
}
