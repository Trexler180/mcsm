package process

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
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
// world's playerdata files).
type Player struct {
	Name     string    `json:"name"`
	UUID     string    `json:"uuid,omitempty"`
	Online   bool      `json:"online"`
	JoinedAt time.Time `json:"joined_at,omitempty"`
	LastSeen time.Time `json:"last_seen,omitempty"`
}

const ringCapacity = 500

type Status string

const (
	StatusOffline     Status = "offline"
	StatusStarting    Status = "starting"
	StatusOnline      Status = "online"
	StatusStopping    Status = "stopping"
	StatusCrashed     Status = "crashed"
	StatusModConflict Status = "mod_conflict"
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

type Instance struct {
	ID     string
	Config StartConfig

	mu        sync.RWMutex
	status    Status
	pid       int
	startedAt time.Time

	cmd   *exec.Cmd
	stdin io.WriteCloser

	broadcastCh chan ConsoleEvent

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

	// Fabric incompatible-mods detection. detector is fed every console line
	// (under conflictMu since stdout/stderr pipes run concurrently); once a
	// conflict is found it's stored in conflict and the process is killed.
	conflictMu sync.Mutex
	detector   conflictDetector
	conflict   *ModConflict

	done chan struct{}
}

func newInstance(id string, cfg StartConfig) *Instance {
	return &Instance{
		ID:             id,
		Config:         cfg,
		status:         StatusStarting,
		broadcastCh:    make(chan ConsoleEvent, 512),
		subs:           make(map[chan ConsoleEvent]struct{}),
		players:        make(map[string]time.Time),
		playersChanged: make(chan struct{}),
		done:           make(chan struct{}),
	}
}

func (inst *Instance) start() error {
	cfg := inst.Config

	javaPath := cfg.JavaBinary
	if javaPath == "" {
		javaPath = "java"
	}

	args := make([]string, 0, len(cfg.JVMArgs)+8+len(cfg.StartArgs))
	args = append(args, cfg.JVMArgs...)

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

	cmd := exec.Command(javaPath, args...)
	cmd.Dir = cfg.Directory

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process: %w", err)
	}

	inst.cmd = cmd
	inst.stdin = stdin
	inst.pid = cmd.Process.Pid
	inst.startedAt = time.Now()
	inst.status = StatusStarting

	go inst.broadcastLoop()
	go inst.pipeStream(stdout, "stdout")
	go inst.pipeStream(stderr, "stderr")
	go inst.waitExit()

	return nil
}

func (inst *Instance) pipeStream(r io.Reader, stream string) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)
	for scanner.Scan() {
		line := scanner.Text()
		event := ConsoleEvent{
			Line:   line,
			Stream: stream,
			TS:     time.Now().UnixMilli(),
		}
		select {
		case inst.broadcastCh <- event:
		default:
		}

		// Watch every line (both streams) for a Fabric incompatible-mods block.
		// On detection, stash the parsed conflict, flip status, and kill the
		// process — it would die on its own, but this captures the diagnostic
		// before waitExit relabels it "crashed".
		inst.conflictMu.Lock()
		mc := inst.detector.feed(line)
		if mc != nil {
			inst.conflict = mc
		}
		inst.conflictMu.Unlock()
		if mc != nil {
			inst.mu.Lock()
			inst.status = StatusModConflict
			inst.mu.Unlock()
			_ = inst.kill()
		}

		if stream == "stdout" {
			if strings.Contains(line, "Done (") {
				inst.mu.Lock()
				if inst.status == StatusStarting {
					inst.status = StatusOnline
				}
				inst.mu.Unlock()
			}
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
}

func (inst *Instance) waitExit() {
	err := inst.cmd.Wait()
	close(inst.broadcastCh)

	inst.mu.Lock()
	prevStatus := inst.status
	if prevStatus == StatusModConflict {
		// Keep the mod-conflict status so the panel can surface suggestions
		// instead of a generic crash.
	} else if err != nil && prevStatus != StatusStopping {
		inst.status = StatusCrashed
	} else {
		inst.status = StatusOffline
	}
	inst.pid = 0
	inst.mu.Unlock()

	inst.subsMu.Lock()
	for ch := range inst.subs {
		close(ch)
	}
	inst.subs = make(map[chan ConsoleEvent]struct{})
	inst.subsMu.Unlock()

	// Server stopped — drop the player roster so a stale list doesn't show
	// in the UI for an offline server.
	inst.playersMu.Lock()
	inst.players = make(map[string]time.Time)
	inst.markPlayersChangedLocked()
	inst.playersMu.Unlock()

	close(inst.done)
}

// exited reports whether the underlying process has already finished (done is
// closed by waitExit). Used to make stop/kill idempotent once it's dead.
func (inst *Instance) exited() bool {
	select {
	case <-inst.done:
		return true
	default:
		return false
	}
}

func (inst *Instance) stop(graceful bool, timeout time.Duration) error {
	// Already dead (e.g. a mod-conflict exit) — nothing to stop, and forcing a
	// kill on a finished process errors with "invalid argument" on Windows.
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
	// Killing an already-finished process returns "invalid argument" on Windows
	// / ErrProcessDone elsewhere — treat a dead process as a successful no-op.
	if inst.exited() {
		return nil
	}
	if inst.cmd != nil && inst.cmd.Process != nil {
		return inst.cmd.Process.Kill()
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
	inst.conflictMu.Unlock()
}
