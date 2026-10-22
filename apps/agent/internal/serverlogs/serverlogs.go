// Package serverlogs reads a Minecraft server's own log files — logs/latest.log
// and the rotated logs/<date>-<n>.log.gz — for the console's "Log files" and
// "Problems" views. The live console stream only covers the current run; these
// files are the server's record of earlier runs, written by the game itself.
//
// Every read is bounded: a file is identified by bare name inside logs/, opened
// through an os.Root so it cannot leave that directory, and decompression stops
// at a fixed size so a hostile .gz cannot fill memory.
package serverlogs

import (
	"bufio"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// LogFile describes one file in logs/.
type LogFile struct {
	Name       string    `json:"name"`
	Size       int64     `json:"size"`
	Modified   time.Time `json:"modified"`
	Compressed bool      `json:"compressed"`
}

// ErrBadName rejects anything but a bare log file name.
var ErrBadName = errors.New("not a log file name")

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.(?:log|txt)(?:\.gz)?$`)

// maxDecompressed bounds how much of a compressed log is ever inflated.
const maxDecompressed = 64 << 20

// maxScan bounds how much of a plain log is read from its end.
const maxScan = 32 << 20

// openLogs opens the server's logs directory through a root on the server
// directory, so a logs/ the server's code replaced with a symlink can't lead
// outside it, to another server's console logs or anywhere else.
func openLogs(serverDir string) (*os.Root, error) {
	server, err := os.OpenRoot(serverDir)
	if err != nil {
		return nil, err
	}
	defer server.Close()
	return server.OpenRoot("logs")
}

// List returns the server's log files, newest first.
func List(serverDir string) ([]LogFile, error) {
	root, err := openLogs(serverDir)
	if errors.Is(err, fs.ErrNotExist) {
		return []LogFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, err
	}
	out := []LogFile{}
	for _, e := range entries {
		if e.IsDir() || !nameRe.MatchString(e.Name()) || e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, LogFile{
			Name: e.Name(), Size: info.Size(), Modified: info.ModTime().UTC(),
			Compressed: strings.HasSuffix(e.Name(), ".gz"),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// open returns a reader over a log file's text, inflating .gz files up to the
// limit.
func open(serverDir, name string) (io.ReadCloser, error) {
	if !nameRe.MatchString(name) {
		return nil, ErrBadName
	}
	root, err := openLogs(serverDir)
	if err != nil {
		return nil, err
	}
	f, err := root.Open(name)
	root.Close()
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(name, ".gz") {
		return f, nil
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{io.LimitReader(gz, maxDecompressed), closers{gz, f}}, nil
}

type closers []io.Closer

func (c closers) Close() error {
	var first error
	for _, cl := range c {
		if err := cl.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Tail returns up to n of the file's last lines. truncated is true when the
// file had more lines than were returned.
func Tail(serverDir, name string, n int) (lines []string, truncated bool, err error) {
	if !nameRe.MatchString(name) {
		return nil, false, ErrBadName
	}
	if !strings.HasSuffix(name, ".gz") {
		return tailPlain(serverDir, name, n)
	}
	rc, err := open(serverDir, name)
	if err != nil {
		return nil, false, err
	}
	defer rc.Close()
	// A compressed file has no end to seek to: stream it, keeping a ring.
	ring := make([]string, 0, n)
	total := 0
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		total++
		if len(ring) < n {
			ring = append(ring, sc.Text())
		} else {
			copy(ring, ring[1:])
			ring[n-1] = sc.Text()
		}
	}
	return ring, total > n, nil
}

func tailPlain(serverDir, name string, n int) ([]string, bool, error) {
	root, err := openLogs(serverDir)
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	const chunk = 64 << 10
	offset := info.Size()
	var buf []byte
	for offset > 0 && info.Size()-offset < maxScan {
		size := min(int64(chunk), offset)
		offset -= size
		b := make([]byte, size)
		if _, err := f.ReadAt(b, offset); err != nil && err != io.EOF {
			return nil, false, err
		}
		buf = append(b, buf...)
		if strings.Count(string(buf), "\n") > n {
			break
		}
	}
	lines := strings.Split(strings.ReplaceAll(string(buf), "\r\n", "\n"), "\n")
	if offset > 0 && len(lines) > 0 {
		lines = lines[1:] // partial first line
	}
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	truncated := offset > 0
	if len(lines) > n {
		lines = lines[len(lines)-n:]
		truncated = true
	}
	return lines, truncated, nil
}

// Problem is one kind of warning or error, with every occurrence grouped.
type Problem struct {
	Level string `json:"level"`
	// Message is the normalized text the occurrences share (numbers, ids and
	// addresses replaced by #).
	Message string `json:"message"`
	// Sample is the first occurrence as written, with the stack trace lines
	// that followed it.
	Sample    string `json:"sample"`
	Count     int    `json:"count"`
	FirstLine int    `json:"first_line"`
	LastLine  int    `json:"last_line"`
	// FirstAt and LastAt are the clock times the log printed (HH:MM:SS).
	FirstAt string `json:"first_at,omitempty"`
	LastAt  string `json:"last_at,omitempty"`
}

// levelRe finds the level bracket of the common log layouts:
//
//	[12:00:00] [Server thread/WARN]: message        (vanilla, Fabric)
//	[12:00:00 WARN]: message                        (Paper, Spigot)
//	[13Oct2026 12:00:00.123] [Server thread/ERROR] [mod/]: message (Forge)
var levelRe = regexp.MustCompile(`^\[([0-9A-Za-z: .]+?)\]?\s*\[?(?:[^\]\[]*/)?(WARN|WARNING|ERROR|FATAL|SEVERE)\](?:\s*\[[^\]]*\])?:?\s*(.*)$`)

var (
	clockRe = regexp.MustCompile(`\b\d{1,2}:\d{2}:\d{2}\b`)
	uuidRe  = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	hexRe   = regexp.MustCompile(`(?i)\b0x[0-9a-f]+\b|@[0-9a-f]{6,}\b`)
	numRe   = regexp.MustCompile(`-?\d+(?:\.\d+)?`)
	spaceRe = regexp.MustCompile(`\s+`)
)

// normalize reduces a message to what its occurrences have in common.
func normalize(msg string) string {
	msg = uuidRe.ReplaceAllString(msg, "#")
	msg = hexRe.ReplaceAllString(msg, "#")
	msg = numRe.ReplaceAllString(msg, "#")
	msg = strings.TrimSpace(spaceRe.ReplaceAllString(msg, " "))
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}

// isContinuation reports a line that belongs to the previous entry. Every log
// entry starts with its "[time" bracket; anything else — the exception line,
// stack frames, "Caused by" — continues the entry above it.
func isContinuation(line string) bool {
	return line != "" && !strings.HasPrefix(line, "[")
}

// maxSampleLines bounds the stack trace kept with a problem's sample.
const maxSampleLines = 40

// Problems groups the file's warnings and errors, most frequent first.
func Problems(serverDir, name string, limit int) ([]Problem, error) {
	rc, err := open(serverDir, name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	if !strings.HasSuffix(name, ".gz") {
		rc = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(rc, maxScan), rc}
	}

	byKey := map[string]*Problem{}
	var order []string
	var current *Problem // the problem whose sample is still collecting a trace
	sampleLines := 0
	lineNo := 0
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		lineNo++
		line := strings.TrimRight(sc.Text(), "\r")
		if current != nil && isContinuation(line) {
			if sampleLines < maxSampleLines {
				current.Sample += "\n" + line
				sampleLines++
			}
			continue
		}
		current = nil
		m := levelRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		level := m[2]
		switch level {
		case "WARNING":
			level = "WARN"
		case "SEVERE", "FATAL":
			level = "ERROR"
		}
		key := level + "|" + normalize(m[3])
		at := clockRe.FindString(m[1])
		p, ok := byKey[key]
		if !ok {
			p = &Problem{Level: level, Message: normalize(m[3]), Sample: line, FirstLine: lineNo, FirstAt: at}
			byKey[key] = p
			order = append(order, key)
			current, sampleLines = p, 0
		}
		p.Count++
		p.LastLine, p.LastAt = lineNo, at
	}
	if err := sc.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return nil, err
	}

	out := make([]Problem, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Level == "ERROR") != (out[j].Level == "ERROR") {
			return out[i].Level == "ERROR"
		}
		return out[i].Count > out[j].Count
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
