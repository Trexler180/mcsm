// Package migrate implements server Minecraft-version migration: move a server
// to a different game version (upgrade or downgrade) as one atomic, reversible
// step. It takes a full backup first, bumps the version and reinstalls the
// runtime jar, swaps every mod to a target-compatible build, disables the mods
// that have no build for the target, then restarts and watches the boot. If the
// server comes up healthy the change sticks; if it doesn't, the backup is
// restored and the panel's DB rows are rewritten so disk and DB stay consistent.
package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/backups"
	"github.com/mcsm/api/internal/mods/hangar"
	"github.com/mcsm/api/internal/mods/modrinth"
	"github.com/mcsm/api/internal/mods/spigotmc"
	"github.com/mcsm/api/internal/store"
)

// ErrAlreadyRunning is returned by Trigger when a migration is in flight for the
// same server.
var ErrAlreadyRunning = errors.New("a version migration is already in progress for this server")

// agentAPI is the slice of agent.Client the engine needs; an interface so tests
// can fake the agent.
type agentAPI interface {
	RegisterDir(ctx context.Context, serverID, directory string) error
	StartServer(ctx context.Context, serverID string, cfg map[string]any) error
	StopServer(ctx context.Context, serverID string, graceful bool, timeoutSec int) error
	GetStatus(ctx context.Context, serverID string) (map[string]any, error)
	Reinstall(ctx context.Context, serverID string, cfg map[string]any) error
	UploadFile(ctx context.Context, serverID, destDir, filename, localPath string) error
	DeleteFile(ctx context.Context, serverID, path string) error
	RenameFile(ctx context.Context, serverID, from, to string) error
	Backup(ctx context.Context, serverID, backupID string) (*agent.BackupResult, error)
	Restore(ctx context.Context, serverID, backupID string) error
}

// modSource is the per-source version lister; every source normalizes into the
// modrinth wire shape.
type modSource interface {
	GetVersions(ctx context.Context, projectID, loader, mcVersion string) ([]modrinth.Version, error)
}

// downloader fetches a file URL to a temp path, verifying SHA256 when provided.
type downloader interface {
	Download(ctx context.Context, fileURL, wantSHA string) (string, error)
}

const disabledSuffix = ".disabled"

// checkableSources can be classified/moved automatically; CurseForge and custom
// jars are left untouched (surfaced as "unmanaged"), matching the preview.
var checkableSources = map[string]bool{"modrinth": true, "hangar": true, "spigotmc": true}

type Engine struct {
	store         *store.Store
	resolveSource func(source string) modSource
	download      downloader
	newAgent      func(n *store.Node) agentAPI

	mu     sync.Mutex
	active map[string]bool

	// Tunables (overridden in tests).
	pollInterval time.Duration
	offlineGrace time.Duration
	stableFor    time.Duration
	startTimeout time.Duration
	runTimeout   time.Duration
}

func New(s *store.Store) *Engine {
	mr := modrinth.New()
	hg := hangar.New()
	sp := spigotmc.New()
	return &Engine{
		store: s,
		resolveSource: func(source string) modSource {
			switch source {
			case "hangar":
				return hg
			case "spigotmc":
				return sp
			default:
				return mr
			}
		},
		download:     mr,
		newAgent:     func(n *store.Node) agentAPI { return agent.New(n.Scheme, n.FQDN, n.Port, n.Token) },
		active:       map[string]bool{},
		pollInterval: 3 * time.Second,
		offlineGrace: 20 * time.Second,
		stableFor:    45 * time.Second,
		startTimeout: 10 * time.Minute,
		runTimeout:   2 * time.Hour,
	}
}

// ── Run progress document ────────────────────────────────────────────

// Per-mod action statuses.
const (
	actUpdate    = "update"    // moved to a target-compatible build
	actDisable   = "disable"   // no build for target: jar disabled
	actUnchanged = "unchanged" // already supports the target, or already disabled
	actUnmanaged = "unmanaged" // custom/CurseForge: left for manual review
	actUnknown   = "unknown"   // upstream lookup failed: left untouched
)

const (
	stepPlanned = "planned"
	stepDone    = "done"
	stepFailed  = "failed"
	stepSkipped = "skipped"
)

type modAction struct {
	ModID       string `json:"mod_id"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	Action      string `json:"action"`
	FromVersion string `json:"from_version,omitempty"`
	ToVersion   string `json:"to_version,omitempty"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
}

type migrationDetail struct {
	Phase      string       `json:"phase"` // checking|backup|applying|verifying|restoring|done
	Message    string       `json:"message,omitempty"`
	FromMC     string       `json:"from_mc"`
	ToMC       string       `json:"to_mc"`
	WasRunning bool         `json:"was_running"`
	BackupID   string       `json:"backup_id,omitempty"`
	Mods       []*modAction `json:"mods"`
}

// plan pairs an installed-mod row with the target build the migration will move
// it to (or, for disables, just the row).
type plan struct {
	mod    *store.InstalledMod
	target *modrinth.Version
	file   *modrinth.VersionFile
	action *modAction
}

type health struct {
	ok     bool
	reason string
}

// ── Public API ───────────────────────────────────────────────────────

// Trigger starts a migration to targetMC for a server and returns the run row
// immediately; the work continues in a detached goroutine and progress is
// written to the row. Returns ErrAlreadyRunning if a run is in flight.
func (e *Engine) Trigger(ctx context.Context, serverID, targetMC string) (*store.VersionMigration, error) {
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

	detail, _ := json.Marshal(migrationDetail{Phase: "checking", FromMC: srv.MCVersion, ToMC: targetMC, Mods: []*modAction{}})
	run, err := e.store.CreateVersionMigration(ctx, serverID, srv.MCVersion, targetMC, detail)
	if err != nil {
		e.release(serverID)
		return nil, err
	}

	go func() {
		defer e.release(serverID)
		runCtx, cancel := context.WithTimeout(context.Background(), e.runTimeout)
		defer cancel()
		e.execute(runCtx, run.ID, srv, node, targetMC)
	}()

	return run, nil
}

func (e *Engine) release(serverID string) {
	e.mu.Lock()
	delete(e.active, serverID)
	e.mu.Unlock()
}

// ── Run execution ────────────────────────────────────────────────────

func (e *Engine) execute(ctx context.Context, runID string, srv *store.Server, node *store.Node, target string) {
	log := slog.With("migration_id", runID, "server_id", srv.ID, "to_mc", target)
	d := &migrationDetail{Phase: "checking", FromMC: srv.MCVersion, ToMC: target, Mods: []*modAction{}}
	var backupID *string
	save := func(status string, finished bool) {
		raw, _ := json.Marshal(d)
		if err := e.store.UpdateVersionMigration(ctx, runID, status, raw, backupID, finished); err != nil {
			log.Error("migrate: persist run", "error", err)
		}
	}
	fail := func(msg string) {
		d.Phase = "done"
		d.Message = msg
		save("failed", true)
		log.Error("migrate: run failed", "message", msg)
		e.audit(srv.ID, "server.migrate_failed", map[string]any{"run_id": runID, "message": msg})
	}

	c := e.newAgent(node)
	if err := c.RegisterDir(ctx, srv.ID, srv.DirectoryPath); err != nil {
		fail("failed to register server directory with agent: " + err.Error())
		return
	}

	// 1. Snapshot the rollback target: server version + every mod row, before any
	// mutation. The filesystem backup restores the jars; these rows restore the DB.
	prevMC := srv.MCVersion
	prevLoader := srv.LoaderVersion
	mods, err := e.store.ListMods(ctx, srv.ID)
	if err != nil {
		fail("list mods: " + err.Error())
		return
	}
	snapshots := make(map[string]store.InstalledMod, len(mods))
	for _, m := range mods {
		snapshots[m.ID] = *m
	}

	// Plan: classify each mod against the target and decide its action.
	toUpdate, toDisable := e.buildPlan(ctx, srv, target, mods, d)
	save("running", false)

	d.WasRunning = e.isRunning(ctx, c, srv.ID)

	// Stop before swapping the jar / mods (replacing files under a live JVM is
	// unsafe and impossible on Windows file locking).
	if d.WasRunning {
		e.stopServer(ctx, c, srv.ID)
	}

	// 2. Backup — the safety net. No backup, no migration.
	d.Phase = "backup"
	d.Message = "creating a restore point before changing the version"
	save("running", false)
	bid, err := e.backup(ctx, c, srv)
	if err != nil {
		if d.WasRunning {
			e.startAndWatch(ctx, c, srv) // best-effort: bring it back up unchanged
		}
		fail("backup failed, nothing was changed: " + err.Error())
		return
	}
	backupID = &bid
	d.BackupID = bid
	save("running", false)

	// 3. Apply: bump version + reinstall jar, then move/disable mods. Any failure
	// from here triggers a restore from the backup we just took.
	d.Phase = "applying"
	d.Message = "changing server version to " + target
	save("running", false)

	srv.MCVersion = target
	if err := e.store.UpdateServer(ctx, srv.ID, srv); err != nil {
		e.rollback(ctx, c, srv, prevMC, prevLoader, snapshots, bid, d, save, "could not record the new version: "+err.Error())
		return
	}
	reinstallCfg := map[string]any{
		"directory":   srv.DirectoryPath,
		"platform":    srv.Platform,
		"mc_version":  target,
		"java_binary": srv.JavaBinary,
	}
	if err := c.Reinstall(ctx, srv.ID, reinstallCfg); err != nil {
		e.rollback(ctx, c, srv, prevMC, prevLoader, snapshots, bid, d, save, "reinstall for "+target+" failed: "+err.Error())
		return
	}

	for _, p := range toUpdate {
		d.Message = "updating " + p.mod.Name
		save("running", false)
		if err := e.applyUpdate(ctx, c, srv.ID, p); err != nil {
			p.action.Status = stepFailed
			p.action.Error = err.Error()
			log.Warn("migrate: mod update failed", "mod", p.mod.Name, "error", err)
			continue
		}
		p.action.Status = stepDone
	}
	for _, p := range toDisable {
		d.Message = "disabling " + p.mod.Name + " (no build for " + target + ")"
		save("running", false)
		if err := e.disableMod(ctx, c, srv.ID, p.mod); err != nil {
			p.action.Status = stepFailed
			p.action.Error = err.Error()
			log.Warn("migrate: mod disable failed", "mod", p.mod.Name, "error", err)
			continue
		}
		p.action.Status = stepDone
	}

	// 4. Verify the boot.
	d.Phase = "verifying"
	d.Message = "restarting on " + target + " and watching the boot"
	save("running", false)
	h := e.startAndWatch(ctx, c, srv)
	if !h.ok {
		log.Warn("migrate: boot unhealthy after migration", "reason", h.reason)
		e.rollback(ctx, c, srv, prevMC, prevLoader, snapshots, bid, d, save, h.reason)
		return
	}

	// Healthy: restore the original power state and close the run.
	if !d.WasRunning {
		e.stopServer(ctx, c, srv.ID)
	}
	e.finishHealthy(d, save)
	e.audit(srv.ID, "server.migrate", map[string]any{
		"run_id": runID, "from": prevMC, "to": target,
		"updated": countAction(d.Mods, actUpdate, stepDone), "disabled": countAction(d.Mods, actDisable, stepDone),
	})
}

// buildPlan classifies every mod against the target version and returns the move
// and disable lists, recording an action per mod on the run detail.
func (e *Engine) buildPlan(ctx context.Context, srv *store.Server, target string, mods []*store.InstalledMod, d *migrationDetail) (toUpdate, toDisable []*plan) {
	loader := modrinth.LoaderForPlatform(srv.Platform)
	for _, m := range mods {
		a := &modAction{ModID: m.ID, Name: m.Name, Source: m.Source, FromVersion: m.Version, Status: stepSkipped}
		d.Mods = append(d.Mods, a)

		if !checkableSources[m.Source] || m.SourceID == nil || m.VersionID == nil {
			a.Action = actUnmanaged
			continue
		}
		versions, err := e.resolveSource(m.Source).GetVersions(ctx, *m.SourceID, loader, target)
		if err != nil {
			a.Action = actUnknown
			continue
		}
		if len(versions) == 0 {
			// No build for the target. Disable it if it's currently loaded;
			// otherwise it's already off and there's nothing to do.
			if m.Enabled {
				a.Action = actDisable
				a.Status = stepPlanned
				toDisable = append(toDisable, &plan{mod: m, action: a})
			} else {
				a.Action = actUnchanged
			}
			continue
		}
		alreadyOK := false
		for i := range versions {
			if versions[i].ID == *m.VersionID {
				alreadyOK = true
				break
			}
		}
		if alreadyOK {
			a.Action = actUnchanged
			continue
		}
		var tgt *modrinth.Version
		var file *modrinth.VersionFile
		for i := range versions {
			if f := primaryFile(&versions[i]); f != nil && f.URL != "" {
				tgt = &versions[i]
				file = f
				break
			}
		}
		if tgt == nil {
			// Builds exist but none downloadable: treat like incompatible.
			if m.Enabled {
				a.Action = actDisable
				a.Status = stepPlanned
				toDisable = append(toDisable, &plan{mod: m, action: a})
			} else {
				a.Action = actUnchanged
			}
			continue
		}
		a.Action = actUpdate
		a.ToVersion = tgt.VersionNumber
		a.Status = stepPlanned
		toUpdate = append(toUpdate, &plan{mod: m, target: tgt, file: file, action: a})
	}
	return toUpdate, toDisable
}

// ── Apply / disable / rollback ───────────────────────────────────────

func (e *Engine) applyUpdate(ctx context.Context, c agentAPI, serverID string, p *plan) error {
	if err := e.swapFile(ctx, c, serverID, p.mod.InstallPath, p.file, p.mod.FileName); err != nil {
		return err
	}
	sha := p.file.Hashes.SHA256
	vid := p.target.ID
	row := *p.mod
	row.VersionID = &vid
	row.Name = p.target.Name
	row.Version = p.target.VersionNumber
	row.FileName = p.file.Filename
	row.SHA256 = &sha
	if _, err := e.store.UpdateMod(ctx, &row); err != nil {
		return fmt.Errorf("record update: %w", err)
	}
	return nil
}

// disableMod renames the jar to "<name>.disabled" on the agent and records the
// new state, so an incompatible mod stops loading without being uninstalled.
func (e *Engine) disableMod(ctx context.Context, c agentAPI, serverID string, m *store.InstalledMod) error {
	if !m.Enabled {
		return nil
	}
	newName := m.FileName + disabledSuffix
	if err := c.RenameFile(ctx, serverID, m.InstallPath+"/"+m.FileName, m.InstallPath+"/"+newName); err != nil {
		return err
	}
	return e.store.SetModEnabled(ctx, m.ID, false, newName)
}

// rollback restores the pre-migration backup and rewrites the snapshotted DB
// rows so disk and DB agree again, then returns the server to its prior power
// state. Marks the run "reverted" (or "failed" if the restore itself fails).
func (e *Engine) rollback(ctx context.Context, c agentAPI, srv *store.Server, prevMC string, prevLoader *string, snapshots map[string]store.InstalledMod, backupID string, d *migrationDetail, save func(string, bool), reason string) {
	log := slog.With("server_id", srv.ID)
	d.Phase = "restoring"
	d.Message = reason + " — restoring the pre-migration backup"
	save("running", false)

	e.stopServer(ctx, c, srv.ID)
	if err := c.Restore(ctx, srv.ID, backupID); err != nil {
		d.Phase = "done"
		d.Message = fmt.Sprintf("%s — and the restore failed: %v. Manual intervention needed; backup id %s", reason, err, backupID)
		save("failed", true)
		log.Error("migrate: restore failed", "error", err)
		e.audit(srv.ID, "server.migrate_failed", map[string]any{"reason": reason, "restore_error": err.Error(), "backup_id": backupID})
		return
	}

	// Disk is back to the pre-migration state; rewrite the DB to match.
	srv.MCVersion = prevMC
	srv.LoaderVersion = prevLoader
	if err := e.store.UpdateServer(ctx, srv.ID, srv); err != nil {
		log.Error("migrate: restore server row", "error", err)
	}
	for id, snap := range snapshots {
		s := snap
		if _, err := e.store.UpdateMod(ctx, &s); err != nil {
			log.Error("migrate: restore mod row", "mod_id", id, "error", err)
		}
		if err := e.store.SetModEnabled(ctx, id, snap.Enabled, snap.FileName); err != nil {
			log.Error("migrate: restore mod enabled", "mod_id", id, "error", err)
		}
	}

	if d.WasRunning {
		e.startAndWatch(ctx, c, srv) // best-effort: it booted before, should again
	} else {
		e.stopServer(ctx, c, srv.ID)
	}

	d.Phase = "done"
	d.Message = reason + " — reverted to " + prevMC
	save("reverted", true)
	e.audit(srv.ID, "server.migrate_reverted", map[string]any{"reason": reason, "from": prevMC})
}

func (e *Engine) finishHealthy(d *migrationDetail, save func(string, bool)) {
	updated := countAction(d.Mods, actUpdate, stepDone)
	disabled := countAction(d.Mods, actDisable, stepDone)
	failed := 0
	for _, a := range d.Mods {
		if a.Status == stepFailed {
			failed++
		}
	}
	status := "success"
	if failed > 0 {
		status = "partial"
	}
	d.Phase = "done"
	d.Message = fmt.Sprintf("migrated to %s: %d updated, %d disabled", d.ToMC, updated, disabled)
	if failed > 0 {
		d.Message += fmt.Sprintf(", %d failed", failed)
	}
	save(status, true)
}

// backup creates a backup row, asks the agent to produce the archive, and marks
// the row's result. Returns the backup id used for a later restore.
func (e *Engine) backup(ctx context.Context, c agentAPI, srv *store.Server) (string, error) {
	b, err := e.store.CreateBackup(ctx, &store.Backup{ServerID: srv.ID, Trigger: "migration", Status: "running"})
	if err != nil {
		return "", err
	}
	res, err := c.Backup(ctx, srv.ID, b.ID)
	if err != nil {
		_ = e.store.UpdateBackupResult(ctx, b.ID, "failed", nil, err.Error())
		return "", err
	}
	_ = e.store.UpdateBackupResult(ctx, b.ID, "success", &res.SizeBytes, "")
	backups.Enforce(ctx, e.store, srv.ID)
	return b.ID, nil
}

// swapFile downloads file (SHA-verified), uploads it into installPath, and
// removes replacesName when the filename changed.
func (e *Engine) swapFile(ctx context.Context, c agentAPI, serverID, installPath string, file *modrinth.VersionFile, replacesName string) error {
	tmp, err := e.download.Download(ctx, file.URL, file.Hashes.SHA256)
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

// ── Server lifecycle helpers ─────────────────────────────────────────

func (e *Engine) isRunning(ctx context.Context, c agentAPI, serverID string) bool {
	status, err := c.GetStatus(ctx, serverID)
	if err != nil {
		return false
	}
	s, _ := status["status"].(string)
	return s == "online" || s == "starting"
}

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
// reached "online" and stayed there for stableFor.
func (e *Engine) startAndWatch(ctx context.Context, c agentAPI, srv *store.Server) health {
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
			if time.Since(onlineSince) >= e.stableFor {
				e.setStatus(srv.ID, "online")
				return health{ok: true}
			}
		case "starting", "stopping":
			sawProcess = true
			onlineSince = time.Time{}
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
		default:
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

// ── small helpers ────────────────────────────────────────────────────

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

func countAction(actions []*modAction, action, status string) int {
	n := 0
	for _, a := range actions {
		if a.Action == action && a.Status == status {
			n++
		}
	}
	return n
}

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
