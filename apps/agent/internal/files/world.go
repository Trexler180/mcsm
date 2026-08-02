package files

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ── World archive import ────────────────────────────────────────────────────
//
// A world on disk is a directory holding level.dat with the region/, data/ and
// dimension trees beside it. People hand us that directory zipped in whatever
// shape their file manager produced: level.dat at the archive root, wrapped in
// one folder, or sitting inside a full server backup next to server.properties.
// Rather than demand one layout, we locate level.dat and treat the directory
// containing it as the world root, ignoring everything outside it.

// maxWorldEntries bounds how many entries we will read from one archive. A real
// world is tens of thousands of region and chunk files at most; anything beyond
// this is malformed or hostile, and refusing up front is cheaper than finding
// out at entry three million.
const maxWorldEntries = 400_000

// maxWorldEntryBytes rejects a single entry claiming an absurd unpacked size,
// so the size estimate can't be pushed into overflow territory by a crafted
// central directory.
const maxWorldEntryBytes = int64(1) << 40 // 1 TiB

// WorldArchive describes where a world lives inside an uploaded zip and how
// much disk it will take once unpacked.
type WorldArchive struct {
	// Prefix is the slash-terminated path inside the archive holding level.dat,
	// or "" when the world sits at the archive root.
	Prefix string
	// SuggestedName is the folder name the archive implies — the wrapping
	// folder's name, or "" when the world is at the archive root and the caller
	// has to supply one.
	SuggestedName string
	// Files and Bytes count only what lies under Prefix.
	Files int
	Bytes int64
}

// ErrNoLevelDat reports an archive that isn't a Minecraft world.
var ErrNoLevelDat = errors.New("this zip doesn't contain a Minecraft world: no level.dat was found inside it")

// ErrMultipleWorlds reports an archive holding several sibling worlds, where
// picking one for the user would be a guess.
var ErrMultipleWorlds = errors.New("this zip contains more than one world; upload a single world folder")

// zipEntryName normalizes an archive entry to a clean, relative, slash-separated
// path. It returns ok=false for anything that tries to escape the extraction
// root (absolute paths, "..", Windows drive letters), which is checked here
// rather than only at write time so such entries never reach the size estimate.
func zipEntryName(name string) (string, bool) {
	n := strings.TrimPrefix(filepath.ToSlash(name), "./")
	if n == "" || strings.HasPrefix(n, "/") || strings.Contains(n, `\`) {
		return "", false
	}
	// A "C:" style prefix is relative on Windows but still points outside.
	if len(n) >= 2 && n[1] == ':' {
		return "", false
	}
	for _, seg := range strings.Split(n, "/") {
		if seg == ".." {
			return "", false
		}
	}
	return strings.TrimSuffix(n, "/"), true
}

// InspectWorldZip reads an archive's directory and reports which part of it is
// a world. It does not extract anything, so the caller can validate, ask for
// confirmation, and check disk space before committing to the write.
func InspectWorldZip(zipPath string) (*WorldArchive, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, errors.New("this file isn't a readable zip archive")
	}
	defer zr.Close()
	return inspectWorldZip(&zr.Reader)
}

func inspectWorldZip(zr *zip.Reader) (*WorldArchive, error) {
	if len(zr.File) > maxWorldEntries {
		return nil, fmt.Errorf("this archive holds %d entries, more than the %d a world import allows",
			len(zr.File), maxWorldEntries)
	}

	// Pick the shallowest directory containing level.dat: in a full server
	// backup that's the world folder rather than the backup root, and in a
	// doubly-wrapped zip it's the inner folder.
	prefix, found, ambiguous := "", false, false
	for _, f := range zr.File {
		name, ok := zipEntryName(f.Name)
		if !ok || f.FileInfo().IsDir() || path.Base(name) != "level.dat" {
			continue
		}
		dir := path.Dir(name)
		if dir == "." {
			dir = ""
		} else {
			dir += "/"
		}
		switch {
		case !found:
			prefix, found = dir, true
		case strings.Count(dir, "/") < strings.Count(prefix, "/"):
			prefix, ambiguous = dir, false
		case strings.Count(dir, "/") == strings.Count(prefix, "/") && dir != prefix:
			ambiguous = true
		}
	}
	if !found {
		return nil, ErrNoLevelDat
	}
	if ambiguous {
		return nil, ErrMultipleWorlds
	}

	arc := &WorldArchive{Prefix: prefix}
	if prefix != "" {
		arc.SuggestedName = path.Base(strings.TrimSuffix(prefix, "/"))
	}
	for _, f := range zr.File {
		name, ok := zipEntryName(f.Name)
		if !ok || f.FileInfo().IsDir() || !strings.HasPrefix(name, prefix) || name == prefix {
			continue
		}
		size := int64(f.UncompressedSize64)
		if f.UncompressedSize64 > uint64(maxWorldEntryBytes) {
			return nil, fmt.Errorf("archive entry %s claims an impossible size", path.Base(name))
		}
		arc.Files++
		arc.Bytes += size
	}
	if arc.Files == 0 {
		return nil, ErrNoLevelDat
	}
	return arc, nil
}

// reservedWindowsNames are device names the Windows filesystem refuses to
// create; the agent runs on Windows too, so a world named "aux" would only fail
// later, deep inside the extraction.
var reservedWindowsNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// CleanWorldName validates a destination folder name for an imported world. It
// deliberately rejects rather than silently rewrites: a world whose folder name
// doesn't match what the user typed is a world they can't find again in
// server.properties.
func CleanWorldName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" {
		return "", errors.New("world name is required")
	}
	if len(n) > 64 {
		return "", errors.New("world name must be 64 characters or fewer")
	}
	if n == "." || n == ".." || strings.HasPrefix(n, ".") {
		return "", errors.New("world name can't start with a dot")
	}
	if strings.HasSuffix(n, ".") || strings.HasSuffix(n, " ") {
		return "", errors.New("world name can't end with a dot or space")
	}
	for _, r := range n {
		if r < 0x20 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return "", errors.New(`world name can't contain / \ : * ? " < > | or control characters`)
		}
	}
	base := strings.ToLower(n)
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	if reservedWindowsNames[base] {
		return "", fmt.Errorf("%q is a reserved name on Windows; pick another", n)
	}
	return n, nil
}

// WorldExists reports whether a directory of this name already sits in the
// server root, so callers can require an explicit replace.
func WorldExists(base, name string) (bool, error) {
	cleanBase, err := secureBase(base)
	if err != nil {
		return false, err
	}
	info, err := os.Stat(filepath.Join(cleanBase, name))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

// ExtractWorld unpacks the world rooted at arc.Prefix inside zipPath into
// <base>/name.
//
// The archive is written to a staging directory and swapped into place only
// once every entry has landed, so a failure part-way through — a truncated
// upload, a full disk — can't leave a half-written world where a working one
// used to be. An existing world of the same name is moved aside and only
// deleted after the swap succeeds.
func ExtractWorld(base, zipPath string, arc *WorldArchive, name string) (err error) {
	cleanBase, err := secureBase(base)
	if err != nil {
		return err
	}
	name, err = CleanWorldName(name)
	if err != nil {
		return err
	}
	target := filepath.Join(cleanBase, name)
	if !withinBase(cleanBase, target) {
		return errors.New("world name escapes the server directory")
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return errors.New("this file isn't a readable zip archive")
	}
	defer zr.Close()

	staging, err := os.MkdirTemp(cleanBase, ".mcsm-world-new-")
	if err != nil {
		return fmt.Errorf("create staging directory: %w", err)
	}
	// Removed on every failure path; on success it has already been renamed
	// away, so this is a no-op.
	defer func() {
		if err != nil {
			os.RemoveAll(staging)
		}
	}()

	for _, f := range zr.File {
		entry, ok := zipEntryName(f.Name)
		if !ok {
			return fmt.Errorf("unsafe path in archive: %s", f.Name)
		}
		if !strings.HasPrefix(entry, arc.Prefix) || entry == arc.Prefix {
			continue
		}
		rel := strings.TrimPrefix(entry, arc.Prefix)
		if rel == "" {
			continue
		}
		dest := filepath.Join(staging, filepath.FromSlash(rel))
		if !withinBase(staging, dest) {
			return fmt.Errorf("unsafe path in archive: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0755); err != nil {
				return fmt.Errorf("create %s: %w", rel, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return fmt.Errorf("create %s: %w", path.Dir(rel), err)
		}
		if err := writeZipEntry(f, dest); err != nil {
			return err
		}
	}

	// Swap: move any existing world aside, put the new one in place, then drop
	// the old copy. Renames are within one directory, so they're atomic and
	// can't half-succeed.
	var parked string
	if _, statErr := os.Lstat(target); statErr == nil {
		parked, err = os.MkdirTemp(cleanBase, ".mcsm-world-old-")
		if err != nil {
			return fmt.Errorf("prepare replacement: %w", err)
		}
		// MkdirTemp made the directory; rename needs the name free.
		os.Remove(parked)
		if err = os.Rename(target, parked); err != nil {
			return fmt.Errorf("move the existing world aside: %w", err)
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("check destination: %w", statErr)
	}

	if err = os.Rename(staging, target); err != nil {
		if parked != "" {
			// Put the original back rather than leaving the server world-less.
			os.Rename(parked, target)
		}
		return fmt.Errorf("install the uploaded world: %w", err)
	}
	if parked != "" {
		os.RemoveAll(parked)
	}
	return nil
}

// writeZipEntry copies one archive entry to dest, bounded by the size the
// archive declared for it. Permissions come from us, not the archive: zips made
// on Windows carry meaningless modes, and a 0000 file would leave the server
// unable to read its own world.
func writeZipEntry(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("read %s from archive: %w", path.Base(f.Name), err)
	}
	defer rc.Close()

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(dest), err)
	}
	declared := int64(f.UncompressedSize64)
	// LimitReader at declared+1 detects an entry that streams more than its
	// header promised — the estimate the disk-space check was made against.
	n, copyErr := io.Copy(out, io.LimitReader(rc, declared+1))
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(dest), copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(dest), closeErr)
	}
	if n > declared {
		return fmt.Errorf("archive entry %s is larger than it declares", path.Base(f.Name))
	}
	return nil
}
