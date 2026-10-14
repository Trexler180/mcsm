package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/lifecycle"
	"github.com/mcsm/api/internal/operations"
	"github.com/mcsm/api/internal/store"
)

// "Why didn't my server start?" The agent recognizes the common startup
// failures in the server's log — incompatible mods, a mod that crashes on
// load, a missing dependency, the wrong Java — and the panel turns each into
// a headline and a few concrete fix plans. Applying a plan is one operation:
// its steps run in order, then the server starts. Plans are always rebuilt on
// the server when applied, so a client can only choose a plan, never write one.

// fixStep is one step of a plan.
type fixStep struct {
	// Kind is "disable_mods", "install_mod", "set_java" or "start".
	Kind      string   `json:"kind"`
	Label     string   `json:"label"`
	ModIDs    []string `json:"mod_ids,omitempty"`
	Source    string   `json:"source,omitempty"`
	ProjectID string   `json:"project_id,omitempty"`
	VersionID string   `json:"version_id,omitempty"`
	JavaPath  string   `json:"java_path,omitempty"`
}

type fixPlan struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Steps       []fixStep `json:"steps"`
	Recommended bool      `json:"recommended"`
	// Permissions the caller needs to apply it; RequiresAdmin when changing
	// what the agent runs.
	Permissions   []string `json:"permissions"`
	RequiresAdmin bool     `json:"requires_admin"`
	// Unresolved names what the plan could not account for (a dependency not
	// found on Modrinth); the plan may still be worth trying.
	Unresolved []string `json:"unresolved,omitempty"`
}

type startupProblem struct {
	Kind         string   `json:"kind"`
	Headline     string   `json:"headline"`
	Detail       string   `json:"detail"`
	Mods         []string `json:"mods"`
	RequiredJava int      `json:"required_java,omitempty"`
	Log          []string `json:"log"`
	DetectedAt   int64    `json:"detected_at"`
	// Live is false when the node could not be asked and this is the last
	// problem the panel recorded; no plans are offered then.
	Live bool `json:"live"`
}

type startupProblemResponse struct {
	Problem *startupProblem `json:"problem"`
	Plans   []fixPlan       `json:"plans"`
}

// agentConflict mirrors the agent's mod_conflict status field.
type agentConflict struct {
	Detected    bool   `json:"detected"`
	Kind        string `json:"kind"`
	Summary     string `json:"summary"`
	Suggestions []struct {
		Action     string   `json:"action"`
		ModID      string   `json:"mod_id"`
		ModName    string   `json:"mod_name"`
		RequiredBy []string `json:"required_by"`
	} `json:"suggestions"`
	RequiredJava int      `json:"required_java"`
	Raw          []string `json:"raw"`
	DetectedAt   int64    `json:"detected_at"`
}

// WithServices gives the mod handlers the operation lock and the lifecycle
// service, which applying a startup fix needs.
func (h *ModHandlers) WithServices(ops *operations.Manager, life *lifecycle.Service) *ModHandlers {
	h.ops, h.life = ops, life
	return h
}

// StartupProblem answers GET /servers/{id}/startup-problem.
func (h *ModHandlers) StartupProblem(w http.ResponseWriter, r *http.Request) {
	srv, err := h.store.GetServer(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeLookupError(w, r, "server", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	problem, plans := h.diagnose(ctx, srv)
	if plans == nil {
		plans = []fixPlan{}
	}
	writeJSON(w, http.StatusOK, startupProblemResponse{Problem: problem, Plans: plans})
}

// diagnose asks the node for the server's startup failure and builds plans.
func (h *ModHandlers) diagnose(ctx context.Context, srv *store.Server) (*startupProblem, []fixPlan) {
	node, err := h.store.GetNode(ctx, srv.NodeID)
	if err != nil {
		return h.recordedProblem(ctx, srv), nil
	}
	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
	status, err := c.GetStatus(ctx, srv.ID)
	if err != nil {
		return h.recordedProblem(ctx, srv), nil
	}
	raw, ok := status["mod_conflict"]
	if !ok || raw == nil {
		return nil, nil
	}
	var conflict agentConflict
	if b, err := json.Marshal(raw); err != nil || json.Unmarshal(b, &conflict) != nil || !conflict.Detected {
		return nil, nil
	}
	problem := describeConflict(&conflict)
	return problem, h.plansFor(ctx, srv, c, &conflict)
}

// recordedProblem is the last open problem the panel stored, for when the
// node cannot be asked.
func (h *ModHandlers) recordedProblem(ctx context.Context, srv *store.Server) *startupProblem {
	list, err := h.store.ListConflicts(ctx, srv.ID, true)
	if err != nil || len(list) == 0 {
		return nil
	}
	c := list[0]
	return &startupProblem{
		Kind: c.Kind, Headline: headlineFor(c.Kind, c.Mods, 0), Detail: c.Summary,
		Mods: c.Mods, Log: []string{}, DetectedAt: c.DetectedAt.Unix(),
	}
}

func describeConflict(c *agentConflict) *startupProblem {
	seen := map[string]bool{}
	var mods []string
	for _, s := range c.Suggestions {
		name := s.ModName
		if name == "" {
			name = s.ModID
		}
		if name != "" && !seen[name] {
			seen[name] = true
			mods = append(mods, name)
		}
	}
	if mods == nil {
		mods = []string{}
	}
	log := c.Raw
	if len(log) > 60 {
		log = log[len(log)-60:]
	}
	if log == nil {
		log = []string{}
	}
	return &startupProblem{
		Kind: c.Kind, Headline: headlineFor(c.Kind, mods, c.RequiredJava), Detail: c.Summary,
		Mods: mods, RequiredJava: c.RequiredJava, Log: log, DetectedAt: c.DetectedAt, Live: true,
	}
}

func headlineFor(kind string, mods []string, java int) string {
	names := strings.Join(mods, ", ")
	switch kind {
	case "java_version":
		if java > 0 {
			return fmt.Sprintf("This server needs Java %d to start", java)
		}
		return "This server needs a different Java version"
	case "missing_dependency":
		if names != "" {
			return "A mod needs something that isn't installed: " + names
		}
		return "A mod needs something that isn't installed"
	case "crash":
		if names != "" {
			return "A mod crashed the server while it was starting: " + names
		}
		return "A mod crashed the server while it was starting"
	default:
		if names != "" {
			return "Some mods can't run together: " + names
		}
		return "Some mods can't run together"
	}
}

// plansFor builds the fix plans for a detected problem.
func (h *ModHandlers) plansFor(ctx context.Context, srv *store.Server, c *agent.Client, conflict *agentConflict) []fixPlan {
	var plans []fixPlan
	start := fixStep{Kind: "start", Label: "Start the server"}

	var disable, disableNames, installIDs []string
	dependents := map[string]bool{}
	for _, s := range conflict.Suggestions {
		switch s.Action {
		case "remove", "replace":
			if s.ModID != "" {
				disable = append(disable, s.ModID)
				disableNames = append(disableNames, firstNonEmpty(s.ModName, s.ModID))
			}
		case "install":
			if s.ModID != "" {
				installIDs = append(installIDs, s.ModID)
			}
			for _, d := range s.RequiredBy {
				dependents[d] = true
			}
		}
	}

	if conflict.Kind == "java_version" && conflict.RequiredJava > 0 {
		if path, version := h.pickJava(ctx, c, conflict.RequiredJava); path != "" {
			plans = append(plans, newPlan(
				fmt.Sprintf("Switch to Java %s and start", version),
				fmt.Sprintf("Run the server with the Java %s already installed on its node.", version),
				true, nil,
				fixStep{Kind: "set_java", Label: "Use " + path, JavaPath: path}, start))
		}
	}

	if len(installIDs) > 0 {
		installed, _ := h.store.ListMods(ctx, srv.ID)
		var steps []fixStep
		var names, unresolved []string
		for _, id := range installIDs {
			res := h.resolveMissingDep(ctx, srv, installed, id)
			if !res.Found || res.VersionID == "" {
				unresolved = append(unresolved, id)
				continue
			}
			title := firstNonEmpty(res.Title, res.Slug, id)
			names = append(names, title)
			steps = append(steps, fixStep{Kind: "install_mod", Label: "Install " + title + " " + res.VersionNumber,
				Source: "modrinth", ProjectID: res.ProjectID, VersionID: res.VersionID})
		}
		if len(steps) > 0 {
			plans = append(plans, newPlan("Install "+joinNames(names)+" and start",
				"Download what the mods need from Modrinth, in a version for this server, then start it.",
				len(unresolved) == 0, unresolved, append(steps, start)...))
		}
		if len(dependents) > 0 {
			var ids []string
			for d := range dependents {
				ids = append(ids, d)
			}
			sort.Strings(ids)
			plans = append(plans, newPlan("Turn off the mods that need them and start",
				"Disable "+joinNames(ids)+" instead of installing anything.",
				len(plans) == 0, nil,
				fixStep{Kind: "disable_mods", Label: "Disable " + joinNames(ids), ModIDs: ids}, start))
		}
	}

	if len(disable) > 0 {
		plans = append(plans, newPlan("Disable "+joinNames(disableNames)+" and start",
			"Turn off the mods the loader named so the rest can run. Their jars are kept, renamed, and can be turned back on.",
			len(plans) == 0, nil,
			fixStep{Kind: "disable_mods", Label: "Disable " + joinNames(disableNames), ModIDs: disable}, start))
	}
	return plans
}

func newPlan(title, description string, recommended bool, unresolved []string, steps ...fixStep) fixPlan {
	p := fixPlan{Title: title, Description: description, Steps: steps, Recommended: recommended, Unresolved: unresolved}
	perms := map[string]bool{}
	for _, s := range steps {
		switch s.Kind {
		case "disable_mods":
			perms[string(store.ServerPermissionModsUpdate)] = true
		case "install_mod":
			perms[string(store.ServerPermissionModsInstall)] = true
		case "set_java":
			p.RequiresAdmin = true
		case "start":
			perms[string(store.ServerPermissionPowerStart)] = true
		}
	}
	for perm := range perms {
		p.Permissions = append(p.Permissions, perm)
	}
	sort.Strings(p.Permissions)
	// The id is a digest of the steps: applying a plan that no longer matches
	// what the server needs fails rather than doing something else.
	b, _ := json.Marshal(steps)
	sum := sha256.Sum256(b)
	p.ID = hex.EncodeToString(sum[:8])
	return p
}

// pickJava chooses an installed runtime for a required major version: the
// exact release if present, else the closest newer one.
func (h *ModHandlers) pickJava(ctx context.Context, c *agent.Client, required int) (string, string) {
	raw, err := c.JavaInstallations(ctx)
	if err != nil {
		return "", ""
	}
	var parsed struct {
		Installations []struct {
			Path    string `json:"path"`
			Version string `json:"version"`
			Major   int    `json:"major"`
		} `json:"installations"`
	}
	if b, err := json.Marshal(raw); err != nil || json.Unmarshal(b, &parsed) != nil {
		return "", ""
	}
	best := -1
	for i, inst := range parsed.Installations {
		if inst.Major < required || inst.Path == "" {
			continue
		}
		if best < 0 || inst.Major < parsed.Installations[best].Major {
			best = i
		}
	}
	if best < 0 {
		return "", ""
	}
	return parsed.Installations[best].Path, fmt.Sprint(parsed.Installations[best].Major)
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	}
	if len(names) > 4 {
		return fmt.Sprintf("%s, %s and %d more", names[0], names[1], len(names)-2)
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// ApplyFix answers POST /servers/{id}/startup-problem/fixes/{planId}/apply:
// it rebuilds the plans, checks the caller may apply the chosen one, and runs
// it as a "fix" operation (202 with the operation).
func (h *ModHandlers) ApplyFix(w http.ResponseWriter, r *http.Request) {
	if h.ops == nil || h.life == nil {
		writeError(w, http.StatusServiceUnavailable, "fixes are unavailable")
		return
	}
	srv, err := h.store.GetServer(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeLookupError(w, r, "server", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	_, plans := h.diagnose(ctx, srv)
	var plan *fixPlan
	for i := range plans {
		if plans[i].ID == chi.URLParam(r, "planId") {
			plan = &plans[i]
		}
	}
	if plan == nil {
		writeCodedError(w, http.StatusConflict, "plan_stale", "the server's problem has changed; reload to see the current fixes")
		return
	}
	if ok, err := h.mayApply(r, srv.ID, plan); err != nil {
		writeServerError(w, r, "apply fix: permissions", err)
		return
	} else if !ok {
		writeError(w, http.StatusForbidden, "you may not apply every step of this fix")
		return
	}

	actor := operations.ActorFromRequest(r, clientIP(r))
	planCopy := *plan
	op, err := h.ops.Run(r.Context(), srv.ID, operations.KindFix, actor, operations.Options{
		Timeout: 30 * time.Minute, Phase: "fixing", Msg: plan.Title,
	}, func(op *operations.Op) (any, error) {
		return nil, h.runPlan(op, srv, actor, planCopy)
	})
	if err != nil {
		writeLifecycleError(w, r, err)
		return
	}
	audit(h.store, r, srv.ID, "server.fix", map[string]any{"plan": plan.Title, "steps": len(plan.Steps), "operation_id": op.ID})
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "fixing", "operation": op})
}

func (h *ModHandlers) mayApply(r *http.Request, serverID string, plan *fixPlan) (bool, error) {
	if auth.MachineFrom(r.Context()) != nil {
		return false, nil
	}
	isAdmin, err := callerIsAdmin(r, h.store)
	if err != nil {
		return false, nil
	}
	if isAdmin {
		return true, nil
	}
	if plan.RequiresAdmin {
		return false, nil
	}
	uid := currentUserID(r)
	for _, perm := range plan.Permissions {
		ok, err := h.store.UserHasServerPermission(r.Context(), uid, serverID, store.ServerPermission(perm))
		if err != nil || !ok {
			return false, err
		}
	}
	return true, nil
}

// runPlan carries out a plan's steps in order inside its operation.
func (h *ModHandlers) runPlan(op *operations.Op, srv *store.Server, actor operations.Actor, plan fixPlan) error {
	ctx := op.Context()
	node, err := h.store.GetNode(ctx, srv.NodeID)
	if err != nil {
		return lifecycle.ErrNodeUnreachable
	}
	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
	if err := c.RegisterDir(ctx, srv.ID, srv.DirectoryPath); err != nil {
		return fmt.Errorf("%w: %v", lifecycle.ErrNodeUnreachable, err)
	}
	for i, step := range plan.Steps {
		frac := float64(i) / float64(len(plan.Steps))
		op.Progress(step.Kind, step.Label, &frac)
		switch step.Kind {
		case "disable_mods":
			disabled, err := c.DisableConflictMods(ctx, srv.ID, step.ModIDs)
			if err != nil {
				return fmt.Errorf("disable mods: %w", err)
			}
			h.markDisabled(ctx, srv.ID, disabled)
			actor.Audit(ctx, h.store, srv.ID, "mod.disable_conflict", map[string]any{"mod_ids": step.ModIDs, "disabled": disabled})
		case "install_mod":
			installed, err := h.installRecursive(ctx, c, srv, step.Source, step.ProjectID, step.VersionID, true, false, map[string]bool{})
			if err != nil {
				return fmt.Errorf("install %s: %w", step.ProjectID, err)
			}
			actor.Audit(ctx, h.store, srv.ID, "mod.install", map[string]any{"source": step.Source, "project_id": step.ProjectID, "count": len(installed)})
		case "set_java":
			if err := h.store.SetServerJavaBinary(ctx, srv.ID, step.JavaPath); err != nil {
				return fmt.Errorf("set java: %w", err)
			}
			actor.Audit(ctx, h.store, srv.ID, "server.update", map[string]any{"java_binary": step.JavaPath})
		case "start":
			_ = h.store.ResolveServerConflicts(ctx, srv.ID)
			current, err := h.store.GetServer(ctx, srv.ID)
			if err != nil {
				return err
			}
			if err := h.life.StartWithin(op, current, actor); err != nil {
				return fmt.Errorf("start: %w", err)
			}
		default:
			return errors.New("unknown step " + step.Kind)
		}
	}
	return nil
}

// markDisabled reflects jars the agent renamed in the panel's mod list.
func (h *ModHandlers) markDisabled(ctx context.Context, serverID string, disabled []string) {
	mods, err := h.store.ListMods(ctx, serverID)
	if err != nil {
		return
	}
	gone := map[string]bool{}
	for _, name := range disabled {
		gone[name] = true
	}
	for _, m := range mods {
		if m.Enabled && gone[m.FileName] {
			_ = h.store.SetModEnabled(ctx, m.ID, false, m.FileName+disabledSuffix)
		}
	}
}
