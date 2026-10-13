package process

import (
	"bytes"
	"io"
	"log"
	"os"
	"strings"
)

// The live console log (.mcsm-run/<id>/console.log) is the server's stdout and
// stderr, written by the JVM itself through an O_APPEND descriptor so output
// survives the agent restarting. Nothing used to bound it: it grew for as long
// as the server ran, and reattaching read the whole file into memory.
//
// It is now rotated by copy-and-truncate. The server cannot be asked to reopen
// its stdout, so renaming the file would leave it writing to the old one;
// truncating in place works because every write is an append at the current
// end. The previous generation is kept as console.log.1. Minecraft's own
// logs/latest.log keeps the full record; this file only feeds the live view.

// maxConsoleLogBytes is the size at which the live console log is rotated.
var maxConsoleLogBytes int64 = 64 << 20

// maxTailScanBytes bounds how far back readLastLines looks for its lines.
const maxTailScanBytes = 8 << 20

func previousLogPath(path string) string { return path + ".1" }

// rotateConsoleLog moves the log's content to its previous generation and
// truncates it. Output the server writes between the copy and the truncate is
// lost from the live view (it is still in logs/latest.log).
func rotateConsoleLog(path string) error {
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := previousLogPath(path) + ".tmp"
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(tmp)
		return err
	}
	if err := dst.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, previousLogPath(path)); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Truncate(path, 0)
}

// readLastLines returns up to n trailing lines of the file (empty on error),
// reading backwards from the end rather than loading the whole file.
func readLastLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	const chunk = 64 << 10
	offset := fi.Size()
	var buf []byte
	for offset > 0 && fi.Size()-offset < maxTailScanBytes {
		size := min(int64(chunk), offset)
		offset -= size
		b := make([]byte, size)
		if _, err := f.ReadAt(b, offset); err != nil && err != io.EOF {
			return nil
		}
		buf = append(b, buf...)
		if bytes.Count(buf, []byte{'\n'}) > n {
			break
		}
	}
	lines := strings.Split(strings.ReplaceAll(string(buf), "\r\n", "\n"), "\n")
	// The first line is partial unless the read reached the start of the file.
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:]
	}
	// Drop a trailing empty element from a final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// logFileState is what the tailer does at the end of the file.
type logFileState int

const (
	logUnchanged logFileState = iota
	// logRestart: the file is now shorter than what was read (rotated or
	// truncated by someone else); read again from its start.
	logRestart
)

// checkConsoleLog runs when the tailer has read everything up to pos. It
// rotates an oversized log and reports a log that shrank underneath it.
func (inst *Instance) checkConsoleLog(pos int64) logFileState {
	fi, err := os.Stat(inst.logPath)
	if err != nil {
		return logUnchanged
	}
	if fi.Size() < pos {
		return logRestart
	}
	if fi.Size() >= maxConsoleLogBytes && fi.Size() == pos {
		if err := rotateConsoleLog(inst.logPath); err != nil {
			log.Printf("[%s] console log rotation failed: %v", inst.ID, err)
			return logUnchanged
		}
		return logRestart
	}
	return logUnchanged
}
