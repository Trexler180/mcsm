package process

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mcsm/agent/internal/java"
)

// Match server-emitted join/leave lines but NOT chat messages. Server lines
// have the player name immediately after `: ` (e.g. `[INFO]: Bob joined the
// game`); chat lines wrap the speaker in `< >` (e.g. `[INFO]: <Bob> ...`).
var (
	playerJoinRe  = regexp.MustCompile(`:\s+([A-Za-z0-9_]{2,16})\s+joined the game\s*$`)
	playerLeaveRe = regexp.MustCompile(`:\s+([A-Za-z0-9_]{2,16})\s+left the game\s*$`)
	playerListRe  = regexp.MustCompile(`:\s*There are\s+\d+(?:\s*/\s*\d+|\s+of a max of\s+\d+)\s+players?\s+online(?::\s*(.*))?\.?\s*$`)
)

// Player is a snapshot of one player. Online players carry JoinedAt (tracked
// live from the console); offline players carry UUID + LastSeen (read from the
// world's playerdata files). The Op/Whitelisted/Banned flags are stamped from
// the server's ops.json/whitelist.json/banned-players.json files.
type Player struct {
	Name     string    `json:"name"`
	UUID     string    `json:"uuid,omitempty"`
	Online   bool      `json:"online"`
	JoinedAt time.Time `json:"joined_at,omitempty"`
	LastSeen time.Time `json:"last_seen,omitempty"`

	Op          bool   `json:"op,omitempty"`
	OpLevel     int    `json:"op_level,omitempty"`
	Whitelisted bool   `json:"whitelisted,omitempty"`
	Banned      bool   `json:"banned,omitempty"`
	BanReason   string `json:"ban_reason,omitempty"`

	// Bedrock is true for players connected through Geyser/Floodgate (Bedrock
	// Edition), detected by Floodgate's UUID signature or username prefix.
	Bedrock bool `json:"bedrock,omitempty"`
}

const ringCapacity = 500

type Status string

const (
	StatusOffline  Status = "offline"
	StatusStarting Status = "starting"
	StatusOnline   Status = "online"
	StatusStopping Status = "stopping"
	StatusCrashed  Status = "crashed"
	// StatusStartupFailure means the process died on startup with a diagnostic we
	// parsed into a fix (incompatible mods, or a mod that crashed the launch).
	StatusStartupFailure Status = "startup_failure"
)

type StartConfig struct {
	Directory  string   `json:"directory"`
	JavaBinary string   `json:"java_binary"`
	JVMArgs    []string `json:"jvm_args"`
	JarFile    string   `json:"jar_file"`
	StartArgs  []string `json:"start_args"`
	// Platform + MCVersion let the agent auto-fetch a server JAR if the
	// directory is empty (paper, purpur, vanilla supported).
	Platform  string `json:"platform,omitempty"`
	MCVersion string `json:"mc_version,omitempty"`
	// NoInstall is set for imported servers: the directory already holds the
	// user's own runtime, so the agent must run it as-is and never auto-download
	// or run an installer over it.
	NoInstall bool `json:"no_install,omitempty"`
}

type ConsoleEvent struct {
	Line   string `json:"line"`
	Stream string `json:"stream"`
	TS     int64  `json:"ts"`
}

type StatusInfo struct {
	ID          string       `json:"id"`
	Status      Status       `json:"status"`
	PID         int          `json:"pid,omitempty"`
	StartedAt   time.Time    `json:"started_at,omitempty"`
	ModConflict *ModConflict `json:"mod_conflict,omitempty"`
}

// launched is the normalized result of starting (or reattaching to) a server
// process, returned by the platform-specific launch(). cmd is non-nil only when
// exit is detected via cmd.Wait (Windows); on the detached path it is nil and we
// watch the PID instead.
type launched struct {
	pid   int
	pgid  int
	stdin io.WriteCloser
	cmd   *exec.Cmd
}

type Instance struct {
	ID         string
	Config     StartConfig
	serverRoot string
	logPath    string

	mu        sync.RWMutex
	status    Status
	pid       int
	pgid      int
	startedAt time.Time

	cmd   *exec.Cmd      // non-nil only when exit is via cmd.Wait (Windows)
	stdin io.WriteCloser // FIFO writer (unix) or stdin pipe (Windows)

	broadcastCh chan ConsoleEvent

	// Lifecycle signals. done is closed once the process has been observed dead
	// and the instance finalized. detaching is closed when the agent is shutting
	// down but deliberately leaving the process running (so the tailer/watcher
	// stop without marking the server offline).
	done      chan struct{}
	detaching chan struct{}

	finalizeOnce sync.Once
	detachOnce   sync.Once

	subsMu sync.Mutex
	subs   map[chan ConsoleEvent]struct{}

	histMu  sync.Mutex
	history []ConsoleEvent

	playersMu sync.Mutex
	players   map[string]time.Time
	// playersChanged is closed/replaced whenever a console event gives us a
	// fresh roster signal. It lets API calls briefly wait for `/list` output.
	playersChanged  chan struct{}
	lastListRefresh time.Time

	// Startup-failure detection. Both detectors are fed every console line
	// (under conflictMu since stdout/stderr pipes run concurrently): detector
	// catches Fabric incompatible-mods blocks, crashDetector catches a mod that
	// crashes startup (e.g. a broken mixin). The first to fire is stored in
	// conflict and the process is killed.
	conflictMu    sync.Mutex
	detector      conflictDetector
	crashDetector mixinCrashDetector
	javaDetector  javaVersionDetector
	conflict      *ModConflict
}

func newInstance(serverRoot, id string, cfg StartConfig) *Instance {
	return &Instance{
		ID:             id,
		Config:         cfg,
		serverRoot:     serverRoot,
		logPath:        consoleLogPath(serverRoot, id),
		status:         StatusStarting,
		broadcastCh:    make(chan ConsoleEvent, 512),
		done:           make(chan struct{}),
		detaching:      make(chan struct{}),
		subs:           make(map[chan ConsoleEvent]struct{}),
		players:        make(map[string]time.Time),
		playersChanged: make(chan struct{}),
	}
}

func (inst *Instance) buildArgs() ([]string, string, string, error) {
	cfg := inst.Config

	javaPath := cfg.JavaBinary
	if javaPath == "" {
		javaPath = "java"
	}
	// Defense in depth: the panel already restricts who can set java_binary, but
	// the agent independently refuses to launch anything that isn't a Java
	// executable, so a bad value can never turn this into "run an arbitrary
	// program". The basename must be java/javaw (optionally .exe); the path itself
	// is otherwise unrestricted so any real JDK location still works.
	if !isJavaBinary(javaPath) {
		return nil, "", "", fmt.Errorf("java_binary must be a java executable, got %q", javaPath)
	}

	// A stored absolute java_binary goes stale when the JDK is upgraded in place —
	// e.g. Adoptium replaces …\jdk-25.0.3.9-hotspot with a newer build-number
	// directory — which would otherwise fail at exec with an opaque "cannot find
	// the path specified". When the configured launcher is missing, fall back to a
	// detected runtime (preferring the major version the config asked for) so the
	// server still starts after a routine JDK update. The security guard above
	// already vetted the configured value; the fallback only ever comes from our
	// own detection, which yields java/javaw launchers only.
	javaPath, javaNote, err := resolveJava(javaPath, java.Detect())
	if err != nil {
		return nil, "", "", err
	}

	args := make([]string, 0, len(cfg.JVMArgs)+8+len(cfg.StartArgs))
	args = append(args, cfg.JVMArgs...)

	// The manager owns the server lifecycle headlessly, so no launcher should
	// ever open a window. Force AWT headless mode: this both suppresses Fabric's
	// Swing "A mod crashed" error GUI (it falls back to console logging when
	// GraphicsEnvironment.isHeadless()) and the vanilla server's Swing GUI.
	// Appended after cfg.JVMArgs so it wins over any user-supplied value.
	args = append(args, "-Djava.awt.headless=true")

	// If install produced a runtime hint (Forge/NeoForge use this for their
	// @args.txt classpath dance), use those args instead of `-jar server.jar`.
	runtimeHint := filepath.Join(cfg.Directory, "mcsm-runtime.txt")
	if data, err := os.ReadFile(runtimeHint); err == nil {
		hint := strings.TrimSpace(string(data))
		if hint != "" {
			args = append(args, strings.Fields(hint)...)
		} else {
			jarFile := cfg.JarFile
			if jarFile == "" {
				jarFile = "server.jar"
			}
			args = append(args, "-jar", jarFile)
		}
	} else {
		jarFile := cfg.JarFile
		if jarFile == "" {
			jarFile = "server.jar"
		}
		args = append(args, "-jar", jarFile)
	}

	args = append(args, cfg.StartArgs...)
	return args, javaPath, javaNote, nil
}

func (inst *Instance) start() error {
	args, javaPath, javaNote, err := inst.buildArgs()
	if err != nil {
		return err
	}

	// Fresh start: ensure the runtime dir exists and clear any leftover console
	// log / stale FIFO from a previous run of this server id.
	if err := os.MkdirAll(runtimeDir(inst.serverRoot, inst.ID), 0o755); err != nil {
		return fmt.Errorf("create runtime dir: %w", err)
	}
	_ = os.Remove(inst.logPath)
	fifoPath := consoleFifoPath(inst.serverRoot, inst.ID)
	_ = os.Remove(fifoPath)

	l, err := launch(javaPath, args, inst.Config.Directory, inst.logPath, fifoPath)
	if err != nil {
		return err
	}

	inst.mu.Lock()
	inst.pid = l.pid
	inst.pgid = l.pgid
	inst.stdin = l.stdin
	inst.cmd = l.cmd
	inst.startedAt = time.Now()
	inst.status = StatusStarting
	inst.mu.Unlock()

	if err := writeRunState(inst.serverRoot, runState{
		ID:        inst.ID,
		PID:       l.pid,
		PGID:      l.pgid,
		StartedAt: inst.startedAt,
		Directory: inst.Config.Directory,
		Config:    inst.Config,
	}); err != nil {
		// Non-fatal: the server is running; we just can't reattach across an agent
		// restart. Surface it on the console so it isn't silent.
		javaNote = strings.TrimSpace(javaNote + "\n[mcsm] warning: could not persist run state: " + err.Error())
	}

	go inst.broadcastLoop()
	if javaNote != "" {
		// Emitted straight onto the broadcast channel (not through the tailer) so
		// it lands in the console history without being scanned by the conflict
		// detectors. Best-effort: drop it if the buffer is somehow full.
		select {
		case inst.broadcastCh <- ConsoleEvent{Line: javaNote, Stream: "stdout", TS: time.Now().UnixMilli()}:
		default:
		}
	}
	go inst.tailLog(0)
	go inst.watchExit()

	return nil
}

// reattachInstance adopts a server that kept running across an agent restart.
// The caller has already confirmed the PID is alive and matches the directory.
func reattachInstance(serverRoot string, st runState) (*Instance, error) {
	inst := newInstance(serverRoot, st.ID, st.Config)

	stdin, err := openStdinWriter(consoleFifoPath(serverRoot, st.ID))
	if err != nil {
		return nil, fmt.Errorf("reopen stdin fifo: %w", err)
	}

	pgid := st.PGID
	if pgid == 0 {
		pgid = st.PID
	}
	inst.mu.Lock()
	inst.pid = st.PID
	inst.pgid = pgid
	inst.stdin = stdin
	inst.startedAt = st.StartedAt
	inst.mu.Unlock()

	// Seed recent console history from the log tail and infer status from it
	// (the live tailer below only streams *new* lines, so it would otherwise miss
	// the "Done (" that already happened before reattach).
	seeded := readLastLines(inst.logPath, ringCapacity)
	now := time.Now().UnixMilli()
	inst.histMu.Lock()
	for _, line := range seeded {
		inst.history = append(inst.history, ConsoleEvent{Line: line, Stream: "stdout", TS: now})
	}
	inst.histMu.Unlock()

	inst.mu.Lock()
	inst.status = inferStatus(seeded)
	inst.mu.Unlock()

	go inst.broadcastLoop()
	go inst.tailLogFromEnd()
	go inst.watchExit()

	return inst, nil
}

// inferStatus guesses a reattached server's state from its recent console: a
// server that already logged "Done (" is online, otherwise still starting. A
// dead process is never reattached, so offline/crashed aren't inferred here.
func inferStatus(lines []string) Status {
	status := StatusStarting
	for _, line := range lines {
		if strings.Contains(line, "Done (") {
			status = StatusOnline
		}
	}
	return status
}

// isJavaBinary reports whether path names a Java launcher (java / javaw, with an
// optional .exe). It only inspects the basename, so any directory is allowed —
// the point is to reject non-Java executables, not to constrain install paths.
func isJavaBinary(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	base = strings.TrimSuffix(base, ".exe")
	return base == "java" || base == "javaw"
}

// resolveJava returns a Java launcher path that actually exists. When the
// configured path resolves (an absolute file that's present, or a bare name
// found on PATH) it's returned unchanged. Otherwise — typically a stored
// absolute path left dangling by an in-place JDK upgrade — it falls back to one
// of the detected runtimes. The returned note, when non-empty, describes the
// substitution for the operator's console. installs is injected (java.Detect())
// so the selection stays unit-testable.
func resolveJava(configured string, installs []java.Installation) (path, note string, err error) {
	if configured == "" {
		configured = "java"
	}
	if _, e := exec.LookPath(configured); e == nil {
		return configured, "", nil
	}
	best := pickFallbackJava(java.MajorFromPath(configured), installs)
	if best == nil {
		return "", "", fmt.Errorf(
			"java not found at %q and no Java runtime is installed — install Java or set the server's Java binary in Options",
			configured,
		)
	}
	return best.Path, fmt.Sprintf(
		"[mcsm] configured Java not found at %s — falling back to %s (Java %d)",
		configured, best.Path, best.Major,
	), nil
}

// pickFallbackJava chooses a replacement runtime from the detected installs,
// preferring an exact feature-version match (so a Java 25 server stays on 25
// rather than dropping to 21) and otherwise the newest available. Returns nil
// when nothing is installed.
func pickFallbackJava(wantMajor int, installs []java.Installation) *java.Installation {
	var newest *java.Installation
	for i := range installs {
		in := &installs[i]
		if wantMajor > 0 && in.Major == wantMajor {
			return in
		}
		if newest == nil || in.Major > newest.Major {
			newest = in
		}
	}
	return newest
}

// tailLog follows the console log file from byte offset start, feeding each
// completed line into consumeLine. It is the single producer for broadcastCh and
// closes it when the process has finalized (so broadcastLoop, and through it the
// subscribers, terminate cleanly).
func (inst *Instance) tailLog(start int64) {
	f, err := os.Open(inst.logPath)
	if err != nil {
		// No log yet (race with first write): retry briefly.
		for i := 0; i < 20 && err != nil; i++ {
			select {
			case <-inst.detaching:
				return
			case <-time.After(100 * time.Millisecond):
			}
			f, err = os.Open(inst.logPath)
		}
		if err != nil {
			return
		}
	}
	defer f.Close()
	if start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			// Tailing from 0 replays old console lines into the ring, which
			// beats silently dropping the tail altogether.
			log.Printf("[%s] tail: seek to %d failed (%v); replaying from start", inst.ID, start, err)
		}
	}

	reader := bufio.NewReader(f)
	var pending string
	for {
		chunk, readErr := reader.ReadString('\n')
		pending += chunk
		if strings.HasSuffix(pending, "\n") {
			inst.consumeLine(strings.TrimRight(pending, "\r\n"), "stdout")
			pending = ""
			continue
		}
		// No complete line yet. If the process has finalized, drain whatever
		// remains once and stop; otherwise wait for more output.
		if readErr == io.EOF {
			select {
			case <-inst.detaching:
				return
			case <-inst.done:
				if strings.TrimSpace(pending) != "" {
					inst.consumeLine(strings.TrimRight(pending, "\r\n"), "stdout")
				}
				close(inst.broadcastCh)
				return
			case <-time.After(200 * time.Millisecond):
			}
			continue
		}
		if readErr != nil {
			return
		}
	}
}

func (inst *Instance) tailLogFromEnd() {
	var size int64
	if fi, err := os.Stat(inst.logPath); err == nil {
		size = fi.Size()
	}
	inst.tailLog(size)
}

// readLastLines returns up to n trailing lines of the file (empty on error).
func readLastLines(path string, n int) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	// Drop a trailing empty element from a final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// consumeLine handles one console line: broadcast it, scan for startup-failure
// diagnostics, the "Done (" online marker, and player join/leave/list events.
func (inst *Instance) consumeLine(line, stream string) {
	event := ConsoleEvent{Line: line, Stream: stream, TS: time.Now().UnixMilli()}
	select {
	case inst.broadcastCh <- event:
	default:
	}

	// Watch every line for a Fabric incompatible-mods block or a mod that crashes
	// startup. On detection, stash the parsed conflict, flip status, and kill the
	// process — it would die on its own, but this captures the diagnostic before
	// the exit watcher relabels it "crashed".
	inst.conflictMu.Lock()
	mc := inst.detector.feed(line)
	if mc == nil {
		mc = inst.crashDetector.feed(line)
	}
	if mc == nil {
		mc = inst.javaDetector.feed(line)
	}
	if mc != nil && inst.conflict == nil {
		inst.conflict = mc
	} else {
		mc = nil // already have one; don't re-kill/relabel
	}
	inst.conflictMu.Unlock()
	if mc != nil {
		inst.mu.Lock()
		inst.status = StatusStartupFailure
		inst.mu.Unlock()
		_ = inst.kill()
	}

	if stream == "stdout" && strings.Contains(line, "Done (") {
		inst.mu.Lock()
		if inst.status == StatusStarting {
			inst.status = StatusOnline
		}
		inst.mu.Unlock()
	}
	if m := playerJoinRe.FindStringSubmatch(line); m != nil {
		inst.playersMu.Lock()
		if _, ok := inst.players[m[1]]; !ok {
			inst.players[m[1]] = time.Now()
			inst.markPlayersChangedLocked()
		}
		inst.playersMu.Unlock()
	} else if m := playerLeaveRe.FindStringSubmatch(line); m != nil {
		inst.playersMu.Lock()
		if _, ok := inst.players[m[1]]; ok {
			delete(inst.players, m[1])
			inst.markPlayersChangedLocked()
		}
		inst.playersMu.Unlock()
	} else if names, ok := parsePlayerListLine(line); ok {
		inst.replacePlayers(names, time.Now())
	}
}

func parsePlayerListLine(line string) ([]string, bool) {
	m := playerListRe.FindStringSubmatch(line)
	if m == nil {
		return nil, false
	}
	if len(m) < 2 || strings.TrimSpace(m[1]) == "" {
		return []string{}, true
	}
	rawNames := strings.Split(m[1], ",")
	names := make([]string, 0, len(rawNames))
	for _, raw := range rawNames {
		name := strings.TrimSpace(raw)
		if name != "" {
			names = append(names, name)
		}
	}
	return names, true
}

func (inst *Instance) markPlayersChangedLocked() {
	close(inst.playersChanged)
	inst.playersChanged = make(chan struct{})
}

func (inst *Instance) replacePlayers(names []string, now time.Time) {
	inst.playersMu.Lock()
	defer inst.playersMu.Unlock()

	previous := make(map[string]time.Time, len(inst.players))
	for name, joined := range inst.players {
		previous[strings.ToLower(name)] = joined
	}

	players := make(map[string]time.Time, len(names))
	for _, name := range names {
		joined := previous[strings.ToLower(name)]
		if joined.IsZero() {
			joined = now
		}
		players[name] = joined
	}
	inst.players = players
	inst.markPlayersChangedLocked()
}

// Players returns a snapshot of currently online players.
func (inst *Instance) Players() []Player {
	inst.playersMu.Lock()
	defer inst.playersMu.Unlock()
	out := make([]Player, 0, len(inst.players))
	for name, joined := range inst.players {
		out = append(out, Player{Name: name, JoinedAt: joined, Online: true})
	}
	return out
}

// RefreshPlayers asks the Minecraft server for its authoritative player list
// and briefly waits for the console response before returning the latest known
// roster. The refresh is throttled because the web UI polls this endpoint.
func (inst *Instance) RefreshPlayers(timeout time.Duration) []Player {
	status := inst.statusInfo().Status
	if status != StatusStarting && status != StatusOnline {
		return inst.Players()
	}

	now := time.Now()
	inst.playersMu.Lock()
	waitCh := inst.playersChanged
	shouldRefresh := now.Sub(inst.lastListRefresh) >= 2*time.Second
	if shouldRefresh {
		inst.lastListRefresh = now
	}
	inst.playersMu.Unlock()

	if shouldRefresh {
		if err := inst.sendCommand("list"); err == nil && timeout > 0 {
			timer := time.NewTimer(timeout)
			select {
			case <-waitCh:
			case <-timer.C:
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
	}
	return inst.Players()
}

func (inst *Instance) broadcastLoop() {
	for event := range inst.broadcastCh {
		inst.histMu.Lock()
		inst.history = append(inst.history, event)
		if len(inst.history) > ringCapacity {
			inst.history = inst.history[len(inst.history)-ringCapacity:]
		}
		inst.histMu.Unlock()

		inst.subsMu.Lock()
		for ch := range inst.subs {
			select {
			case ch <- event:
			default:
			}
		}
		inst.subsMu.Unlock()
	}

	// broadcastCh closed by the tailer after finalize: tear down subscribers so
	// their consumers (console WebSockets) see the stream end.
	inst.subsMu.Lock()
	for ch := range inst.subs {
		close(ch)
	}
	inst.subs = make(map[chan ConsoleEvent]struct{})
	inst.subsMu.Unlock()
}

// watchExit blocks until the process exits, then finalizes the instance — unless
// the agent is detaching (shutting down while leaving the server running), in
// which case it returns quietly.
func (inst *Instance) watchExit() {
	if inst.cmd != nil {
		// Windows/local-dev path: the process is our child; Wait reaps it.
		waited := make(chan struct{})
		go func() { _ = inst.cmd.Wait(); close(waited) }()
		select {
		case <-waited:
			inst.finalize()
		case <-inst.detaching:
		}
		return
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-inst.detaching:
			return
		case <-inst.done:
			return
		case <-ticker.C:
			if !processAlive(inst.pid) {
				inst.finalize()
				return
			}
		}
	}
}

// finalize records that the process has died: it sets the terminal status, drops
// the roster, removes the runtime state, and signals done (which the tailer uses
// to drain and close the broadcast). Runs at most once.
func (inst *Instance) finalize() {
	inst.finalizeOnce.Do(func() {
		inst.mu.Lock()
		prev := inst.status
		switch {
		case prev == StatusStartupFailure:
			// keep the diagnostic status so the panel can surface a fix
		case prev == StatusStopping:
			inst.status = StatusOffline
		default:
			inst.status = StatusCrashed
		}
		inst.pid = 0
		stdin := inst.stdin
		inst.mu.Unlock()

		if stdin != nil {
			_ = stdin.Close()
		}

		inst.playersMu.Lock()
		inst.players = make(map[string]time.Time)
		inst.markPlayersChangedLocked()
		inst.playersMu.Unlock()

		// The process is gone; its runtime files are no longer reattachable.
		clearRunState(inst.serverRoot, inst.ID)

		close(inst.done)
	})
}

// detach stops tracking the process without stopping it. Used on agent shutdown
// so a deploy/restart leaves Minecraft servers running for the next agent to
// reattach to.
func (inst *Instance) detach() {
	inst.detachOnce.Do(func() {
		close(inst.detaching)
		inst.mu.RLock()
		stdin := inst.stdin
		inst.mu.RUnlock()
		if stdin != nil {
			_ = stdin.Close()
		}
	})
}

// exited reports whether the process has already finished (done is closed by
// finalize). Used to make stop/kill idempotent once it's dead.
func (inst *Instance) exited() bool {
	select {
	case <-inst.done:
		return true
	default:
		return false
	}
}

func (inst *Instance) stop(graceful bool, timeout time.Duration) error {
	// Already dead (e.g. a mod-conflict exit) — nothing to stop.
	if inst.exited() {
		return nil
	}

	inst.mu.Lock()
	inst.status = StatusStopping
	inst.mu.Unlock()

	if graceful {
		if err := inst.sendCommand("stop"); err == nil {
			select {
			case <-inst.done:
				return nil
			case <-time.After(timeout):
			}
		}
	}
	return inst.kill()
}

func (inst *Instance) kill() error {
	if inst.exited() {
		return nil
	}
	inst.mu.RLock()
	pid, pgid := inst.pid, inst.pgid
	inst.mu.RUnlock()
	if pid <= 0 {
		return nil
	}
	if err := terminate(pid, pgid, true); err != nil {
		return err
	}
	// Give the exit watcher a moment to observe the death and finalize, so callers
	// (stop, Restart) see consistent state rather than racing the 1s poll.
	select {
	case <-inst.done:
	case <-time.After(5 * time.Second):
	}
	return nil
}

func (inst *Instance) sendCommand(cmd string) error {
	inst.mu.RLock()
	stdin := inst.stdin
	inst.mu.RUnlock()
	if stdin == nil {
		return fmt.Errorf("stdin not available")
	}
	_, err := fmt.Fprintf(stdin, "%s\n", cmd)
	return err
}

func (inst *Instance) subscribe() (<-chan ConsoleEvent, func()) {
	ch := make(chan ConsoleEvent, 512)

	// Dead instance: hand back an already-closed channel so the consumer ends.
	if inst.exited() {
		close(ch)
		return ch, func() {}
	}

	inst.histMu.Lock()
	hist := make([]ConsoleEvent, len(inst.history))
	copy(hist, inst.history)
	inst.histMu.Unlock()

	inst.subsMu.Lock()
	inst.subs[ch] = struct{}{}
	inst.subsMu.Unlock()

	go func() {
		for _, e := range hist {
			select {
			case ch <- e:
			default:
			}
		}
	}()

	return ch, func() {
		inst.subsMu.Lock()
		delete(inst.subs, ch)
		inst.subsMu.Unlock()
	}
}

func (inst *Instance) statusInfo() StatusInfo {
	inst.mu.RLock()
	info := StatusInfo{
		ID:        inst.ID,
		Status:    inst.status,
		PID:       inst.pid,
		StartedAt: inst.startedAt,
	}
	inst.mu.RUnlock()

	inst.conflictMu.Lock()
	info.ModConflict = inst.conflict
	inst.conflictMu.Unlock()
	return info
}

// Conflict returns the detected mod conflict, or nil.
func (inst *Instance) Conflict() *ModConflict {
	inst.conflictMu.Lock()
	defer inst.conflictMu.Unlock()
	return inst.conflict
}

// ClearConflict drops a stored conflict (after the user applies a fix) and
// resets the detector so a fresh start can detect a new one.
func (inst *Instance) ClearConflict() {
	inst.conflictMu.Lock()
	inst.conflict = nil
	inst.detector = conflictDetector{}
	inst.crashDetector = mixinCrashDetector{}
	inst.javaDetector = javaVersionDetector{}
	inst.conflictMu.Unlock()
}
