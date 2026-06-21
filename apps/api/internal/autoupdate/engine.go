// Package autoupdate implements safe automatic mod updates: apply every
// pending update, restart the server, and watch the boot. If the server comes
// up healthy the updates stick. If it crashes, the engine reverts the updates,
// isolates which one broke the boot by re-applying them one at a time, marks
// the offending version as skipped (never auto-installed again), and leaves
// the server running on the last known-good set.
package autoupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/store"
)

// ErrAlreadyRunning is returned by Trigger when a run is in flight for the
// same server.
var ErrAlreadyRunning = errors.New("an auto-update run is already in progress for this server")

// agentAPI is the slice of agent.Client the engine needs; an interface so
// tests can fake the agent.
type agentAPI interface {
	RegisterDir(ctx context.Context, serverID, directory string) error
	StartServer(ctx context.Context, serverID string, cfg map[string]any) error
	StopServer(ctx context.Context, serverID string, graceful bool, timeoutSec int) error
	GetStatus(ctx context.Context, serverID string) (map[string]any, error)
	UploadFile(ctx context.Context, serverID, destDir, filename, localPath string) error
	DeleteFile(ctx context.Context, serverID, path string) error
}

// modSource is the slice of modrinth.Client the engine needs.
type modSource interface {
	GetVersions(ctx context.Context, projectID, loader, mcVersion string) ([]modrinth.Version, error)
	GetVersion(ctx context.Context, versionID string) (*modrinth.Version, error)
	Download(ctx context.Context, fileURL, wantSHA string) (string, error)
}

type Engine struct {
	store    *store.Store
	modrinth modSource
	newAgent func(n *store.Node) agentAPI

	mu     sync.Mutex
	active map[string]bool // serverID -> run in flight

	// Tunables (overridden in tests).
	pollInterval time.Duration // status poll cadence during boot watch
	offlineGrace time.Duration // how long "offline" is tolerated right after start
	stableFor    time.Duration // how long the server must stay online to count as healthy
	startTimeout time.Duration // give up waiting for "online" after this
	runTimeout   time.Duration // hard cap for a whole run incl. isolation restarts
}

func New(s *store.Store) *Engine {
	return &Engine{
		store:        s,
		modrinth:     modrinth.New(),
		newAgent:     func(n *store.Node) agentAPI { return agent.New(n.Scheme, n.FQDN, n.Port, n.Token) },
		active:       map[string]bool{},
		pollInterval: 3 * time.Second,
		offlineGrace: 20 * time.Second,
		stableFor:    45 * time.Second,
		startTimeout: 10 * time.Minute,
		runTimeout:   2 * time.Hour,
	}
}

// ── Run progress document (stored as mod_update_runs.detail) ─────────

// Step statuses for one mod inside a run.
const (
	stepPending         = "pending"          // update selected, not yet final
	stepUpdated         = "updated"          // new version kept
	stepRevertedSkipped = "reverted_skipped" // broke the boot: reverted + version blocklisted
	stepFailed          = "failed"           // apply/revert itself errored (network, agent)
)

type modStep struct {
	ModID       string `json:"mod_id"`
	ProjectID   string `json:"project_id"`
	Name        string `json:"name"`
	FromVersion string `json:"from_version"`
	ToVersion   string `json:"to_version"`
	ToVersionID string `json:"to_version_id"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}

type runDetail struct {
	Phase      string     `json:"phase"` // checking|applying|verifying|isolating|reverting|restoring|done
	Message    string     `json:"message,omitempty"`
	WasRunning bool       `json:"was_running"`
	Mods       []*modStep `json:"mods"`
}

// candidate pairs an installed mod row with the version the run will move it to,
// keeping the old row state so the move can be undone.
type candidate struct {
	mod    *store.InstalledMod // snapshot BEFORE the update (revert target)
	target *modrinth.Version
	file   *modrinth.VersionFile
	step   *modStep
}

// health is the verdict of one boot watch.
type health struct {
	ok     bool
	reason string
}

// ── Public API ───────────────────────────────────────────────────────

// Trigger starts an auto-update run for a server and returns its run row
// immediately; the work continues in the background and progress is written to
// the row. Returns ErrAlreadyRunning if a run is in flight for this server.
func (e *Engine) Trigger(ctx context.Context, serverID, trigger string) (*store.ModUpdateRun, error) {
	srv, err := e.store.GetServer(ctx, serverID)
	if err != nil {
		return nil, fmt.Errorf("server not found")
	}
	node, err := e.store.GetNode(ctx, srv.NodeID)
	if err != nil {
		return nil, fmt.Errorf("node not found")
	}

	e.mu.Lock()
	if e.active[serverID] {
		e.mu.Unlock()
		return nil, ErrAlreadyRunning
	}
	e.active[serverID] = true
	e.mu.Unlock()

	detail, _ := json.Marshal(runDetail{Phase: "checking", Mods: []*modStep{}})
	run, err := e.store.CreateModUpdateRun(ctx, serverID, trigger, detail)
	if err != nil {
		e.release(serverID)
		return nil, err
	}

	go func() {
		defer e.release(serverID)
		// Detached from the request: closing the browser must not abort a run
		// that may be mid-revert.
		runCtx, cancel := context.WithTimeout(context.Background(), e.runTimeout)
		defer cancel()
		e.execute(runCtx, run.ID, srv, node)
	}()

	return run, nil
}

func (e *Engine) release(serverID string) {
	e.mu.Lock()
	delete(e.active, serverID)
	e.mu.Unlock()
}

// PickUpdate returns the newest version that is not blocklisted, or nil when
// the mod is already on it. versions must be sorted newest-first (Modrinth's
// API order). Walking newest→oldest: the first acceptable version found before
// reaching the current one is the update; reaching the current version first
// means every newer version is skipped.
func PickUpdate(versions []modrinth.Version, currentVersionID string, skipped map[string]bool) *modrinth.Version {
	for i := range versions {
		v := &versions[i]
		if v.ID == currentVersionID {
			return nil
		}
		if skipped[v.ID] {
			continue
		}
		return v
	}
	return nil
}

// ── Run execution ────────────────────────────────────────────────────

func (e *Engine) execute(ctx context.Context, runID string, srv *store.Server, node *store.Node) {
	log := slog.With("run_id", runID, "server_id", srv.ID)
	d := &runDetail{Phase: "checking", Mods: []*modStep{}}
	save := func(status string, finished bool) {
		raw, _ := json.Marshal(d)
		if err := e.store.UpdateModUpdateRun(ctx, runID, status, raw, finished); err != nil {
			log.Error("autoupdate: persist run", "error", err)
		}
	}
	fail := func(msg string) {
		d.Phase = "done"
		d.Message = msg
		save("failed", true)
		log.Error("autoupdate: run failed", "message", msg)
		e.audit(srv.ID, "mod.autoupdate_failed", map[string]any{"run_id": runID, "message": msg})
	}

	c := e.newAgent(node)
	if err := c.RegisterDir(ctx, srv.ID, srv.DirectoryPath); err != nil {
		fail("failed to register server directory with agent: " + err.Error())
		return
	}

	candidates, err := e.findCandidates(ctx, srv, d)
	if err != nil {
		fail(err.Error())
		return
	}
	if len(candidates) == 0 {
		d.Phase = "done"
		d.Message = "everything is up to date"
		save("no_updates", true)
		return
	}
	save("running", false)

	d.WasRunning = e.isRunning(ctx, c, srv.ID)

	// Stop before swapping jars: replacing a jar under a live JVM is unsafe
	// (and impossible on Windows file locking).
	if d.WasRunning {
		d.Phase = "applying"
		d.Message = "stopping server to apply updates"
		save("running", false)
		e.stopServer(ctx, c, srv.ID)
	}

	// Apply every update. A mod whose download/upload fails is recorded and
	// left on its old version; the run continues with the rest.
	d.Phase = "applying"
	var applied []*candidate
	for _, cand := range candidates {
		d.Message = "updating " + cand.mod.Name
		save("running", false)
		if err := e.applyUpdate(ctx, c, srv.ID, cand); err != nil {
			cand.step.Status = stepFailed
			cand.step.Error = err.Error()
			log.Warn("autoupdate: apply failed", "mod", cand.mod.Name, "error", err)
			continue
		}
		applied = append(applied, cand)
	}

	if len(applied) == 0 {
		d.Phase = "restoring"
		d.Message = "no updates could be applied"
		save("running", false)
		if d.WasRunning {
			e.startAndWatch(ctx, c, srv, nil) // best-effort: bring it back up
		}
		fail("no updates could be applied")
		return
	}

	// Boot with the new versions and watch.
	d.Phase = "verifying"
	d.Message = fmt.Sprintf("restarting with %d update(s) and watching the boot", len(applied))
	save("running", false)
	h := e.startAndWatch(ctx, c, srv, func(msg string) {
		d.Message = msg
		save("running", false)
	})

	if h.ok {
		for _, cand := range applied {
			cand.step.Status = stepUpdated
		}
		e.finishHealthy(ctx, c, srv, d, save)
		e.audit(srv.ID, "mod.autoupdate", map[string]any{"run_id": runID, "updated": len(applied)})
		return
	}

	// Boot is broken. Revert and (when several mods updated) isolate the culprit.
	log.Warn("autoupdate: boot unhealthy after updates", "reason", h.reason)

	if len(applied) == 1 {
		cand := applied[0]
		d.Phase = "reverting"
		d.Message = fmt.Sprintf("%s — reverting %s", h.reason, cand.mod.Name)
		save("running", false)
		if err := e.revertUpdate(ctx, c, srv.ID, cand); err != nil {
			fail(fmt.Sprintf("server broke after updating %s (%s) and the revert failed: %v — manual intervention needed", cand.mod.Name, h.reason, err))
			return
		}
		e.markSkipped(ctx, srv.ID, cand, h.reason)
		cand.step.Status = stepRevertedSkipped
		cand.step.Error = h.reason

		if rb := e.startAndWatch(ctx, c, srv, nil); !rb.ok {
			fail(fmt.Sprintf("reverted %s but the server is still unhealthy: %s", cand.mod.Name, rb.reason))
			return
		}
		e.finishHealthy(ctx, c, srv, d, save)
		e.audit(srv.ID, "mod.autoupdate_reverted", map[string]any{"run_id": runID, "mod": cand.mod.Name, "skipped_version": cand.target.VersionNumber, "reason": h.reason})
		return
	}

	// Several updates went in together: roll all of them back, confirm the old
	// set still boots, then re-apply one at a time to find the culprit(s).
	d.Phase = "reverting"
	d.Message = h.reason + " — reverting all updates to isolate the cause"
	save("running", false)
	e.stopServer(ctx, c, srv.ID)
	for _, cand := range applied {
		if err := e.revertUpdate(ctx, c, srv.ID, cand); err != nil {
			fail(fmt.Sprintf("revert of %s failed: %v — manual intervention needed", cand.mod.Name, err))
			return
		}
	}
	if base := e.startAndWatch(ctx, c, srv, nil); !base.ok {
		fail(fmt.Sprintf("server is unhealthy even with all updates reverted (%s) — the failure is not caused by these updates; nothing was blocklisted", base.reason))
		return
	}

	d.Phase = "isolating"
	for _, cand := range applied {
		d.Message = "testing " + cand.mod.Name + " " + cand.target.VersionNumber
		save("running", false)

		e.stopServer(ctx, c, srv.ID)
		if err := e.applyUpdate(ctx, c, srv.ID, cand); err != nil {
			cand.step.Status = stepFailed
			cand.step.Error = "re-apply during isolation failed: " + err.Error()
			if rb := e.startAndWatch(ctx, c, srv, nil); !rb.ok {
				fail("server did not come back during isolation: " + rb.reason)
				return
			}
			continue
		}

		ih := e.startAndWatch(ctx, c, srv, nil)
		if ih.ok {
			cand.step.Status = stepUpdated
			continue
		}

		// This update breaks the boot: take it back out and blocklist it.
		log.Warn("autoupdate: isolated culprit", "mod", cand.mod.Name, "version", cand.target.VersionNumber, "reason", ih.reason)
		e.stopServer(ctx, c, srv.ID)
		if err := e.revertUpdate(ctx, c, srv.ID, cand); err != nil {
			fail(fmt.Sprintf("revert of %s failed during isolation: %v — manual intervention needed", cand.mod.Name, err))
			return
		}
		e.markSkipped(ctx, srv.ID, cand, ih.reason)
		cand.step.Status = stepRevertedSkipped
		cand.step.Error = ih.reason
		if rb := e.startAndWatch(ctx, c, srv, nil); !rb.ok {
			fail("server did not come back after reverting " + cand.mod.Name + ": " + rb.reason)
			return
		}
	}

	e.finishHealthy(ctx, c, srv, d, save)
	e.audit(srv.ID, "mod.autoupdate_isolated", map[string]any{"run_id": runID, "skipped": countStatus(d.Mods, stepRevertedSkipped), "updated": countStatus(d.Mods, stepUpdated)})
}

// finishHealthy restores the original power state (the server is online here),
// computes the terminal status from the per-mod outcomes, and closes the run.
func (e *Engine) finishHealthy(ctx context.Context, c agentAPI, srv *store.Server, d *runDetail, save func(string, bool)) {
	if !d.WasRunning {
		d.Phase = "restoring"
		d.Message = "stopping server (it was offline before the run)"
		save("running", false)
		e.stopServer(ctx, c, srv.ID)
	}

	updated := countStatus(d.Mods, stepUpdated)
	reverted := countStatus(d.Mods, stepRevertedSkipped)
	failed := countStatus(d.Mods, stepFailed)

	status := "success"
	switch {
	case reverted > 0 && updated > 0:
		status = "partial"
	case reverted > 0:
		status = "reverted"
	case failed > 0:
		status = "partial"
	}

	d.Phase = "done"
	parts := []string{}
	if updated > 0 {
		parts = append(parts, fmt.Sprintf("%d updated", updated))
	}
	if reverted > 0 {
		parts = append(parts, fmt.Sprintf("%d reverted and blocklisted", reverted))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed to apply", failed))
	}
	d.Message = strings.Join(parts, ", ")
	save(status, true)
}

func countStatus(steps []*modStep, status string) int {
	n := 0
	for _, s := range steps {
		if s.Status == status {
			n++
		}
	}
	return n
}

// ── Update selection ─────────────────────────────────────────────────

// findCandidates lists every installed Modrinth mod that has a newer compatible
// version which is not blocklisted. Pinned, disabled, and custom/CurseForge
// mods are left alone.
func (e *Engine) findCandidates(ctx context.Context, srv *store.Server, d *runDetail) ([]*candidate, error) {
	mods, err := e.store.ListMods(ctx, srv.ID)
	if err != nil {
		return nil, fmt.Errorf("list mods: %w", err)
	}
	skiplist, err := e.store.ListSkippedModVersions(ctx, srv.ID)
	if err != nil {
		return nil, fmt.Errorf("list skipped versions: %w", err)
	}
	skipped := map[string]map[string]bool{}
	for _, s := range skiplist {
		if skipped[s.ProjectID] == nil {
			skipped[s.ProjectID] = map[string]bool{}
		}
		skipped[s.ProjectID][s.VersionID] = true
	}

	loader := modrinth.LoaderForPlatform(srv.Platform)
	var out []*candidate
	for _, m := range mods {
		if m.Source != "modrinth" || m.SourceID == nil || m.VersionID == nil || m.Pinned || !m.Enabled {
			continue
		}
		versions, err := e.modrinth.GetVersions(ctx, *m.SourceID, loader, srv.MCVersion)
		if err != nil || len(versions) == 0 {
			continue // transient lookup failure: this mod just isn't updated this run
		}
		target := PickUpdate(versions, *m.VersionID, skipped[*m.SourceID])
		if target == nil {
			continue
		}
		file := primaryFile(target)
		if file == nil || file.URL == "" {
			continue // nothing downloadable
		}
		snapshot := *m
		step := &modStep{
			ModID:       m.ID,
			ProjectID:   *m.SourceID,
			Name:        m.Name,
			FromVersion: m.Version,
			ToVersion:   target.VersionNumber,
			ToVersionID: target.ID,
			Status:      stepPending,
		}
		d.Mods = append(d.Mods, step)
		out = append(out, &candidate{mod: &snapshot, target: target, file: file, step: step})
	}
	return out, nil
}

func primaryFile(ver *modrinth.Version) *modrinth.VersionFile {
	for i := range ver.Files {
		if ver.Files[i].Primary {
			return &ver.Files[i]
		}
	}
	if len(ver.Files) > 0 {
		return &ver.Files[0]
	}
	return nil
}

// ── Jar swapping ─────────────────────────────────────────────────────

// applyUpdate downloads the candidate's target version, swaps the jar on the
// agent, and points the DB row at the new version.
func (e *Engine) applyUpdate(ctx context.Context, c agentAPI, serverID string, cand *candidate) error {
	old := cand.mod
	if err := e.swapFile(ctx, c, serverID, old.InstallPath, cand.file, old.FileName); err != nil {
		return err
	}
	sha := cand.file.Hashes.SHA256
	vid := cand.target.ID
	row := *old
	row.VersionID = &vid
	row.Name = cand.target.Name
	row.Version = cand.target.VersionNumber
	row.FileName = cand.file.Filename
	row.SHA256 = &sha
	if _, err := e.store.UpdateMod(ctx, &row); err != nil {
		return fmt.Errorf("record update: %w", err)
	}
	return nil
}

// revertUpdate re-installs the version the mod had before the run (the old
// jar was deleted during apply, so it is re-downloaded from the source).
func (e *Engine) revertUpdate(ctx context.Context, c agentAPI, serverID string, cand *candidate) error {
	old := cand.mod
	if old.VersionID == nil {
		return fmt.Errorf("no previous version recorded")
	}
	prev, err := e.modrinth.GetVersion(ctx, *old.VersionID)
	if err != nil {
		return fmt.Errorf("look up previous version: %w", err)
	}
	file := primaryFile(prev)
	if file == nil || file.URL == "" {
		return fmt.Errorf("previous version has no downloadable file")
	}
	if err := e.swapFile(ctx, c, serverID, old.InstallPath, file, cand.file.Filename); err != nil {
		return err
	}
	if _, err := e.store.UpdateMod(ctx, old); err != nil {
		return fmt.Errorf("restore mod row: %w", err)
	}
	return nil
}

// swapFile downloads file (SHA-verified), uploads it into installPath, and
// removes replacesName when the filename changed.
func (e *Engine) swapFile(ctx context.Context, c agentAPI, serverID, installPath string, file *modrinth.VersionFile, replacesName string) error {
	tmp, err := e.modrinth.Download(ctx, file.URL, file.Hashes.SHA256)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(tmp)
	if err := c.UploadFile(ctx, serverID, installPath, file.Filename, tmp); err != nil {
		return err
	}
	if replacesName != "" && replacesName != file.Filename {
		_ = c.DeleteFile(ctx, serverID, installPath+"/"+replacesName)
	}
	return nil
}

func (e *Engine) markSkipped(ctx context.Context, serverID string, cand *candidate, reason string) {
	err := e.store.AddSkippedModVersion(ctx, &store.SkippedModVersion{
		ServerID:  serverID,
		ProjectID: cand.step.ProjectID,
		VersionID: cand.target.ID,
		ModName:   cand.mod.Name,
		Version:   cand.target.VersionNumber,
		Reason:    reason,
	})
	if err != nil {
		slog.Error("autoupdate: blocklist version", "server_id", serverID, "mod", cand.mod.Name, "error", err)
	}
}

// ── Server lifecycle helpers ─────────────────────────────────────────

func (e *Engine) isRunning(ctx context.Context, c agentAPI, serverID string) bool {
	status, err := c.GetStatus(ctx, serverID)
	if err != nil {
		return false
	}
	s, _ := status["status"].(string)
	return s == "online" || s == "starting"
}

// stopServer asks for a graceful stop and waits until the process is gone.
// Errors are swallowed: the follow-up start surfaces any real problem.
func (e *Engine) stopServer(ctx context.Context, c agentAPI, serverID string) {
	_ = c.StopServer(ctx, serverID, true, 30)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		status, err := c.GetStatus(ctx, serverID)
		if err != nil {
			break
		}
		s, _ := status["status"].(string)
		if s != "online" && s != "stopping" && s != "starting" {
			break
		}
		if !sleepCtx(ctx, e.pollInterval) {
			break
		}
	}
	e.setStatus(serverID, "offline")
}

// startAndWatch boots the server and classifies the result: healthy means it
// reached "online" and stayed there for stableFor; crashed / startup_failure /
// process-exit / timeout are unhealthy with a reason.
// onProgress, when non-nil, is called each poll with a human-readable status so
// the caller can surface live boot progress (notably the stable-for countdown
// once the server reaches "online"). It is invoked from this goroutine only.
func (e *Engine) startAndWatch(ctx context.Context, c agentAPI, srv *store.Server, onProgress func(string)) health {
	cfg := agent.StartConfig(srv.DirectoryPath, srv.JavaBinary, srv.JVMArgs, srv.Platform, srv.MCVersion, srv.RAMMbMin, srv.RAMMbMax)
	e.setStatus(srv.ID, "starting")
	if err := c.StartServer(ctx, srv.ID, cfg); err != nil {
		e.setStatus(srv.ID, "offline")
		return health{reason: "start failed: " + err.Error()}
	}

	started := time.Now()
	var onlineSince time.Time
	sawProcess := false
	agentErrors := 0

	for {
		if !sleepCtx(ctx, e.pollInterval) {
			return health{reason: "run cancelled while waiting for the server to come online"}
		}
		status, err := c.GetStatus(ctx, srv.ID)
		if err != nil {
			agentErrors++
			if agentErrors >= 10 {
				return health{reason: "agent became unreachable while watching the boot"}
			}
			continue
		}
		agentErrors = 0
		s, _ := status["status"].(string)

		switch s {
		case "online":
			sawProcess = true
			if onlineSince.IsZero() {
				onlineSince = time.Now()
			}
			stable := time.Since(onlineSince)
			if onProgress != nil {
				secs := int(stable.Seconds())
				total := int(e.stableFor.Seconds())
				if secs > total {
					secs = total
				}
				onProgress(fmt.Sprintf("server online — confirming it stays up (%ds/%ds)", secs, total))
			}
			if stable >= e.stableFor {
				e.setStatus(srv.ID, "online")
				return health{ok: true}
			}
		case "starting", "stopping":
			sawProcess = true
			onlineSince = time.Time{}
			if onProgress != nil {
				onProgress(fmt.Sprintf("waiting for the server to come online (%ds)", int(time.Since(started).Seconds())))
			}
		case "crashed":
			e.setStatus(srv.ID, "crashed")
			return health{reason: "server crashed during startup"}
		case "startup_failure":
			reason := "a mod prevented the server from starting"
			if mc, ok := status["mod_conflict"].(map[string]any); ok {
				if summary, ok := mc["summary"].(string); ok && summary != "" {
					reason = "startup failed: " + summary
				}
			}
			e.setStatus(srv.ID, "startup_failure")
			return health{reason: reason}
		default: // offline or unknown
			// A short offline window right after start is normal while the
			// agent spins up the process; offline after the process existed
			// means it died without the agent flagging a crash.
			if sawProcess || time.Since(started) > e.offlineGrace {
				e.setStatus(srv.ID, "offline")
				return health{reason: "server process exited during startup"}
			}
		}

		if time.Since(started) > e.startTimeout {
			return health{reason: fmt.Sprintf("server did not come online within %s", e.startTimeout)}
		}
	}
}

// setStatus mirrors the agent state into the panel DB so the UI tracks the
// run live; the poller would converge eventually anyway.
func (e *Engine) setStatus(serverID, status string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = e.store.UpdateServerStatus(ctx, serverID, status)
}

func (e *Engine) audit(serverID, action string, detail map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	e.store.LogAction(ctx, "", serverID, action, "", detail)
}

// sleepCtx sleeps for d, returning false if ctx ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
