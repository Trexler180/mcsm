package files

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// The agent writes a few files into a server's directory itself, before the
// server starts and with the agent's own privileges: server.properties,
// eula.txt, the helper mod jar. With isolation on, the server's own code can
// write that directory too, so any of those names may be a symlink it planted,
// pointing at the panel's secrets or database. These helpers touch such files
// only through an os.Root on the server directory, which refuses paths that
// leave it, and only when the file itself is a regular file, never a link.

// ErrNotRegular reports a path that exists but isn't a plain file: a symlink,
// a directory, a device. The agent refuses to read or replace those.
var ErrNotRegular = errors.New("not a regular file")

// ReadRegular reads dir/rel, refusing a symlink or anything else that isn't a
// plain file inside dir. A missing file reports fs.ErrNotExist. limit > 0 caps
// how much is read.
func ReadRegular(dir, rel string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return readRegularIn(root, rel, limit)
}

func readRegularIn(root *os.Root, rel string, limit int64) ([]byte, error) {
	before, err := root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %w", rel, ErrNotRegular)
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// The name could have been swapped between the Lstat and the Open; what was
	// opened must be the file that was checked.
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) {
		return nil, fmt.Errorf("%s: %w", rel, ErrNotRegular)
	}
	var r io.Reader = f
	if limit > 0 {
		r = io.LimitReader(f, limit)
	}
	return io.ReadAll(r)
}

// WriteRegularAtomic replaces dir/rel with body through a fresh temp file and a
// rename, so a crash never leaves it half written. An existing rel must be a
// regular file (its permissions are kept); a symlink or other non-file is
// refused rather than followed. The rename replaces the name itself, so even a
// link planted at the last moment is replaced, never written through.
func WriteRegularAtomic(dir, rel string, body []byte, mode fs.FileMode) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return writeRegularAtomicIn(root, rel, body, mode)
}

func writeRegularAtomicIn(root *os.Root, rel string, body []byte, mode fs.FileMode) error {
	if info, err := root.Lstat(rel); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s: %w", rel, ErrNotRegular)
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	tmp, err := tempName(rel)
	if err != nil {
		return err
	}
	// O_EXCL: the temp name is new and random, so nothing can be waiting there.
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		_ = root.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	if err := root.Rename(tmp, rel); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

// CreateRegularIfMissing writes dir/rel only when nothing exists at that name,
// creating it exclusively so a dangling symlink there is never followed.
// created is false when something was already there (left untouched).
func CreateRegularIfMissing(dir, rel string, body []byte, mode fs.FileMode) (created bool, err error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false, err
	}
	defer root.Close()
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := f.Write(body); err != nil {
		f.Close()
		return false, err
	}
	return true, f.Close()
}

func tempName(rel string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return rel + ".mcsm-" + hex.EncodeToString(b[:]) + ".tmp", nil
}

// MkdirAllIn creates dir/rel and its parents through an os.Root on dir, so a
// symlinked component can't take the new directories outside it.
func MkdirAllIn(dir, rel string, perm fs.FileMode) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.MkdirAll(rel, perm)
}
