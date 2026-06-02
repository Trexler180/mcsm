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
)

// Player is a snapshot of one online player.
type Player struct {
	Name     string    `json:"name"`
	JoinedAt time.Time `json:"joined_at"`
}

const ringCapacity = 500

type Status string

const (
	StatusOffline  Status = "offline"
	StatusStarting Status = "starting"
	StatusOnline   Status = "online"
	StatusStopping Status = "stopping"
	StatusCrashed  Status = "crashed"
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
	ID        string    `json:"id"`
	Status    Status    `json:"status"`
	PID       int       `json:"pid,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
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

	done chan struct{}
}

func newInstance(id string, cfg StartConfig) *Instance {
	return &Instance{
		ID:          id,
		Config:      cfg,
		status:      StatusStarting,
		broadcastCh: make(chan ConsoleEvent, 512),
		subs:        make(map[chan ConsoleEvent]struct{}),
		players:     make(map[string]time.Time),
		done:        make(chan struct{}),
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

		if stream == "stdout" {
			if strings.Contains(line, "Done (") {
				inst.mu.Lock()
				if inst.status == StatusStarting {
					inst.status = StatusOnline
				}
				inst.mu.Unlock()
			}
			if m := playerJoinRe.FindStringSubmatch(line); m != nil {
				inst.playersMu.Lock()
				inst.players[m[1]] = time.Now()
				inst.playersMu.Unlock()
			} else if m := playerLeaveRe.FindStringSubmatch(line); m != nil {
				inst.playersMu.Lock()
				delete(inst.players, m[1])
				inst.playersMu.Unlock()
			}
		}
	}
}

// Players returns a snapshot of currently online players.
func (inst *Instance) Players() []Player {
	inst.playersMu.Lock()
	defer inst.playersMu.Unlock()
	out := make([]Player, 0, len(inst.players))
	for name, joined := range inst.players {
		out = append(out, Player{Name: name, JoinedAt: joined})
	}
	return out
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
	if err != nil && prevStatus != StatusStopping {
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
	inst.playersMu.Unlock()

	close(inst.done)
}

func (inst *Instance) stop(graceful bool, timeout time.Duration) error {
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
	defer inst.mu.RUnlock()
	return StatusInfo{
		ID:        inst.ID,
		Status:    inst.status,
		PID:       inst.pid,
		StartedAt: inst.startedAt,
	}
}
