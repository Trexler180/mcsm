package files

import (
	"archive/zip"
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
