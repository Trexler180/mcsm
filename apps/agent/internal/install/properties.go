package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PropertiesFile is the config every Java-edition server reads its listen port
// (and most other settings) from.
const PropertiesFile = "server.properties"

// ApplyServerPort makes server.properties agree with the port the panel holds
// for this server.
//
// The panel's port used to be metadata only — nothing ever wrote it to disk. A
// server created on, say, 25570 therefore generated a stock server.properties
// on its first boot, listened on vanilla's 25565, and left the panel pinging a
// port nothing was bound to. Applying the port here (on directory setup, when
// it changes, and again before every launch) makes the value the operator
// picked the value the JVM actually binds.
//
// Everything else in the file is preserved verbatim: comments, ordering, and
// keys this panel knows nothing about. query.port follows the change only when
// it was tracking the old server port, so an intentionally separate query port
// is left alone.
//
// changed reports whether the file was written; previous is the port that was
// configured before (0 when the file or the key did not exist).
func ApplyServerPort(dir string, port int) (changed bool, previous int, err error) {
	if port <= 0 || port > 65535 {
		return false, 0, fmt.Errorf("invalid port %d", port)
	}
	path := filepath.Join(dir, PropertiesFile)
	want := strconv.Itoa(port)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		// No file yet (freshly created server). Writing only the port is enough:
		// the server fills in every key it doesn't find on first boot, so this
		// short file becomes a complete one — with the operator's port in it.
		body := "# Created by the server manager. The server fills in the remaining\n" +
			"# defaults the first time it starts.\n" +
			"server-port=" + want + "\n" +
			"query.port=" + want + "\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return false, 0, err
		}
		return true, 0, nil
	}
	if err != nil {
		return false, 0, err
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if k, v, ok := splitProperty(line); ok && k == "server-port" {
			previous, _ = strconv.Atoi(v)
			break
		}
	}

	sawPort := false
	for i, line := range lines {
		k, v, ok := splitProperty(line)
		if !ok {
			continue
		}
		switch k {
		case "server-port":
			sawPort = true
			if v != want {
				lines[i] = rewriteProperty(line, k, want)
				changed = true
			}
		case "query.port":
			// Only when it was following server-port. A query port deliberately
			// pointed somewhere else is the operator's decision, not ours.
			if previous > 0 && v == strconv.Itoa(previous) && v != want {
				lines[i] = rewriteProperty(line, k, want)
				changed = true
			}
		}
	}

	body := strings.Join(lines, "\n")
	if !sawPort {
		if body != "" && !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
		body += "server-port=" + want + "\n"
		changed = true
	}
	if !changed {
		return false, previous, nil
	}
	if err := writePreservingMode(path, []byte(body)); err != nil {
		return false, previous, err
	}
	return true, previous, nil
}

// ReadServerPort returns the port configured in a directory's
// server.properties, or ok=false when the file or the key is absent.
func ReadServerPort(dir string) (int, bool) {
	v, ok := readProperties(filepath.Join(dir, PropertiesFile))["server-port"]
	if !ok {
		return 0, false
	}
	port, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || port <= 0 || port > 65535 {
		return 0, false
	}
	return port, true
}

// splitProperty breaks one server.properties line into its key and value,
// reporting ok=false for blanks, comments, and anything that isn't a pair.
func splitProperty(line string) (key, value string, ok bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "!") {
		return "", "", false
	}
	k, v, found := strings.Cut(trimmed, "=")
	if !found {
		return "", "", false
	}
	return strings.TrimSpace(k), strings.TrimSpace(v), true
}

// rewriteProperty replaces a line's value while keeping the line ending the
// file already used, so editing one key in a CRLF file doesn't mix endings.
func rewriteProperty(line, key, value string) string {
	if strings.HasSuffix(line, "\r") {
		return key + "=" + value + "\r"
	}
	return key + "=" + value
}

// writePreservingMode replaces a file's contents through a temp file in the
// same directory, so a crash mid-write can't truncate a live server's config.
func writePreservingMode(path string, body []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp := path + ".mcsm-tmp"
	if err := os.WriteFile(tmp, body, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
