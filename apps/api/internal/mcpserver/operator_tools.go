package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mcsm/api/internal/store"
)

var safeOperatorID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,160}$`)

type lifecycleOutput struct {
	Server ServerRef `json:"server"`
	Action string    `json:"action"`
	Status string    `json:"status"`
	Note   string    `json:"note"`
}

type operatorOutput struct {
	Server ServerRef `json:"server"`
	// Result is encoded JSON rather than an unconstrained Go any. The MCP SDK
	// otherwise emits an empty schema for this property, which Claude Code
	// rejects while loading the tool list. Keeping the outer value a string
	// gives every client a concrete schema while preserving the bounded,
	// sanitized structured payload from the internal handler.
	Result string `json:"result,omitempty"`
	Note   string `json:"note,omitempty"`
}

type modSearchInput struct {
	ServerID string `json:"server_id" jsonschema:"a server id from list_servers"`
	// Name, not "query": this is a mod/plugin name matched against approved
	// sources, and naming it "query" would read like a free-form search string
	// the facade forwards somewhere. It also keeps the escape-hatch guard in
	// service_test.go honest rather than carving an exception into it.
	Name   string `json:"name" jsonschema:"mod or plugin name to search for, at most 120 characters"`
	Source string `json:"source,omitempty" jsonschema:"modrinth, curseforge, hangar, or spigotmc; defaults to modrinth"`
	Limit  int    `json:"limit,omitempty" jsonschema:"results to return, 1-20; defaults to 10"`
}

type installModInput struct {
	ServerID         string `json:"server_id" jsonschema:"a server id from list_servers"`
	Source           string `json:"source,omitempty" jsonschema:"modrinth, curseforge, hangar, or spigotmc; defaults to modrinth"`
	ProjectID        string `json:"project_id" jsonschema:"project id returned by find_compatible_mods"`
	VersionID        string `json:"version_id,omitempty" jsonschema:"specific compatible version id; omit for newest compatible"`
	WithDependencies bool   `json:"with_dependencies,omitempty" jsonschema:"also install required dependencies"`
}

type updateModInput struct {
	ServerID  string `json:"server_id" jsonschema:"a server id from list_servers"`
	ModID     string `json:"mod_id" jsonschema:"installed mod id returned by list_server_mods"`
	VersionID string `json:"version_id,omitempty" jsonschema:"specific compatible version; omit for newest compatible update"`
}

type setModEnabledInput struct {
	ServerID                    string `json:"server_id" jsonschema:"a server id from list_servers"`
	ModID                       string `json:"mod_id" jsonschema:"installed mod id returned by list_server_mods"`
	Enabled                     bool   `json:"enabled" jsonschema:"true to enable, false to disable"`
	AcknowledgeDependencyImpact bool   `json:"acknowledge_dependency_impact,omitempty" jsonschema:"required when disabling a mod that other installed content needs"`
	DisableDependents           bool   `json:"disable_dependents,omitempty" jsonschema:"also disable installed dependents so the next boot remains consistent"`
}

type removeModInput struct {
	ServerID                    string `json:"server_id" jsonschema:"a server id from list_servers"`
	ModID                       string `json:"mod_id" jsonschema:"installed mod id returned by list_server_mods"`
	AcknowledgeDependencyImpact bool   `json:"acknowledge_dependency_impact,omitempty" jsonschema:"required when other installed content needs this mod"`
	DisableDependents           bool   `json:"disable_dependents,omitempty" jsonschema:"also disable dependents after removing this mod"`
}

type createBackupInput struct {
	ServerID string `json:"server_id" jsonschema:"a server id from list_servers"`
}

const backupConfirmationID = "confirm_backup"

func (svc *Service) registerOperatorTools(s *mcp.Server, p *Principal) {
	if p.HasScope(store.MCPScopePowerStart) {
		mcp.AddTool(s, &mcp.Tool{Name: "start_server", Title: "Start server", Description: "Start one selected server immediately. This executes without another browser approval because the connection was explicitly granted start authority.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false), IdempotentHint: true}}, svc.directLifecycle(p, "start", store.MCPScopePowerStart, store.ServerPermissionPowerStart))
	}
	if p.HasScope(store.MCPScopePowerStop) {
		mcp.AddTool(s, &mcp.Tool{Name: "stop_server", Title: "Stop server", Description: "Gracefully stop one selected server immediately.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false), IdempotentHint: true}}, svc.directLifecycle(p, "stop", store.MCPScopePowerStop, store.ServerPermissionPowerStop))
	}
	if p.HasScope(store.MCPScopePowerRestart) {
		mcp.AddTool(s, &mcp.Tool{Name: "restart_server", Title: "Restart server", Description: "Restart one selected server immediately after a bounded graceful stop.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false), IdempotentHint: false}}, svc.directLifecycle(p, "restart", store.MCPScopePowerRestart, store.ServerPermissionPowerRestart))
	}
	if p.HasScope(store.MCPScopeModsRead) && svc.operator != nil {
		mcp.AddTool(s, &mcp.Tool{Name: "list_server_mods", Title: "List installed mods", Description: "List installed mods/plugins and their enabled, version, source, and dependency state.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, svc.listServerMods(p))
		mcp.AddTool(s, &mcp.Tool{Name: "find_compatible_mods", Title: "Find compatible mods", Description: "Search approved mod sources using the selected server platform and Minecraft version. It accepts no URL.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, svc.findCompatibleMods(p))
		mcp.AddTool(s, &mcp.Tool{
			Name: "list_minecraft_versions", Title: "List installable Minecraft versions",
			Description: "The Minecraft releases, or loader builds, that can actually be installed for this " +
				"server's platform. Confirm an upgrade target here first: a version that was never released is " +
				"accepted by the upgrade request and only fails during the migration, after a restore point " +
				"has already been taken.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		}, svc.listMinecraftVersions(p))
		mcp.AddTool(s, &mcp.Tool{
			Name: "list_mod_update_history", Title: "List mod update history",
			Description: "Past update runs for this server, or the versions deliberately skipped. A skipped " +
				"version is a decision, not an oversight — check here before reporting a mod as out of date.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		}, svc.listModUpdateHistory(p))
		mcp.AddTool(s, &mcp.Tool{Name: "list_mod_updates", Title: "List mod updates", Description: "Check installed source-backed mods for compatible, non-blocklisted updates.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, svc.listModUpdates(p))
		mcp.AddTool(s, &mcp.Tool{
			Name: "resolve_missing_dependencies", Title: "Resolve missing dependencies",
			Description: "Map mod ids from a missing_dependency blocker to installable projects for this server's " +
				"platform and Minecraft version. Use this instead of searching by name: several loader ids do not " +
				"match their project slug (a jar declaring \"fabric\" is published as \"fabric-api\"), so a search " +
				"finds the wrong project or nothing. Read-only — it resolves, it does not install. Feed the " +
				"project_id and version_id it returns to install_mod.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		}, svc.resolveMissingDependencies(p))
	}
	if p.HasScope(store.MCPScopeModsInstall) && svc.operator != nil {
		mcp.AddTool(s, &mcp.Tool{Name: "install_mod", Title: "Install mod", Description: "Install a compatible source project on one selected server. Do not infer permission to create a backup from this operation.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false), IdempotentHint: false}}, svc.installMod(p))
	}
	if p.HasScope(store.MCPScopeModsUpdate) && svc.operator != nil {
		mcp.AddTool(s, &mcp.Tool{Name: "update_mod", Title: "Update mod", Description: "Replace one installed source-backed mod with a compatible version.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false), IdempotentHint: false}}, svc.updateMod(p))
		mcp.AddTool(s, &mcp.Tool{Name: "set_mod_enabled", Title: "Enable or disable mod", Description: "Enable or disable one installed mod. Dependency-breaking changes are refused unless explicitly acknowledged.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false), IdempotentHint: true}}, svc.setModEnabled(p))
	}
	if p.HasScope(store.MCPScopeModsRemove) && svc.operator != nil {
		mcp.AddTool(s, &mcp.Tool{Name: "remove_mod", Title: "Remove mod", Description: "Remove one installed mod. Dependency-breaking removal is refused unless explicitly acknowledged.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(true), IdempotentHint: false}}, svc.removeMod(p))
	}
	if p.HasScope(store.MCPScopeBackupsCreate) && svc.operator != nil {
		mcp.AddTool(s, &mcp.Tool{Name: "create_server_backup", Title: "Create server backup", Description: "Request a full, potentially long and expensive server backup. This never starts from the agent's request alone: the MCP client must obtain a separate human confirmation for this exact server, and unsupported clients fail closed.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false), IdempotentHint: false}}, svc.createServerBackup(p))
		mcp.AddTool(s, &mcp.Tool{Name: "list_server_backups", Title: "List server backups", Description: "List bounded backup status records. This reads status only and never creates a backup.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, svc.listServerBackups(p))
	}
	svc.registerConsoleTools(s, p)
}

func (svc *Service) directLifecycle(p *Principal, action string, scope store.MCPScope, permission store.ServerPermission) mcp.ToolHandlerFor[serverIDInput, lifecycleOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in serverIDInput) (*mcp.CallToolResult, lifecycleOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, scope, permission, false)
		if err != nil {
			return nil, lifecycleOutput{}, err
		}
		if err := svc.runAction(ctx, srv, action); err != nil {
			svc.auditOperator(ctx, p, srv.ID, "server."+action+"_failed", map[string]any{"error": clean(err.Error(), 240)})
			return nil, lifecycleOutput{}, toolError("%s failed", action)
		}
		svc.auditOperator(ctx, p, srv.ID, "server."+action, nil)
		return nil, lifecycleOutput{Server: serverRef(srv), Action: action, Status: "accepted", Note: "The node accepted the operation. Poll diagnostics and bounded log events to verify the resulting state."}, nil
	}
}

func (svc *Service) listServerMods(p *Principal) mcp.ToolHandlerFor[serverIDInput, operatorOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in serverIDInput) (*mcp.CallToolResult, operatorOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeModsRead, store.ServerPermissionMods, true)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		result, err := svc.invokeOperator(ctx, p, "mods.list", srv.ID, "", nil)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		return nil, operatorOutput{Server: serverRef(srv), Result: result}, nil
	}
}

func (svc *Service) findCompatibleMods(p *Principal) mcp.ToolHandlerFor[modSearchInput, operatorOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in modSearchInput) (*mcp.CallToolResult, operatorOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeModsRead, store.ServerPermissionMods, true)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		query := strings.TrimSpace(in.Name)
		if query == "" || len(query) > 120 {
			return nil, operatorOutput{}, toolError("name must be 1-120 characters")
		}
		source, err := operatorSource(in.Source)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		projectType := "mod"
		if srv.Platform == "paper" || srv.Platform == "spigot" || srv.Platform == "purpur" {
			projectType = "plugin"
		}
		args := map[string]any{"query": query, "source": source, "loader": srv.Platform, "mc_version": srv.MCVersion, "project_type": projectType, "limit": bound(in.Limit, 10, 20), "offset": 0}
		result, err := svc.invokeOperator(ctx, p, "mods.search", srv.ID, "", args)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		return nil, operatorOutput{Server: serverRef(srv), Result: result, Note: "Results are compatibility-filtered for this server; use the returned source and project id with install_mod."}, nil
	}
}

func (svc *Service) listModUpdates(p *Principal) mcp.ToolHandlerFor[serverIDInput, operatorOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in serverIDInput) (*mcp.CallToolResult, operatorOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeModsRead, store.ServerPermissionMods, true)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		result, err := svc.invokeOperator(ctx, p, "mods.updates", srv.ID, "", nil)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		return nil, operatorOutput{Server: serverRef(srv), Result: result}, nil
	}
}

type resolveMissingDepsInput struct {
	ServerID string `json:"server_id" jsonschema:"a server id from list_servers"`
	// ModIDs, not a free-form query: these are the loader ids a blocker named,
	// passed through unchanged so the resolution is of the actual failure rather
	// than of something the model retyped.
	ModIDs []string `json:"mod_ids" jsonschema:"the mod ids from a missing_dependency blocker, at most 25"`
}

func (svc *Service) resolveMissingDependencies(p *Principal) mcp.ToolHandlerFor[resolveMissingDepsInput, operatorOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in resolveMissingDepsInput) (*mcp.CallToolResult, operatorOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeModsRead, store.ServerPermissionMods, true)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		ids := make([]string, 0, len(in.ModIDs))
		for _, id := range in.ModIDs {
			id = strings.TrimSpace(id)
			// A loader mod id is a short slug-shaped token. Reusing the operator-id
			// rule keeps a path, a URL, or a sentence out of the lookup.
			if id == "" || !validOperatorID(id) {
				continue
			}
			ids = append(ids, id)
			if len(ids) == 25 {
				break
			}
		}
		if len(ids) == 0 {
			return nil, operatorOutput{}, toolError("mod_ids must contain at least one mod id from a missing_dependency blocker")
		}
		result, err := svc.invokeOperator(ctx, p, "mods.resolve_missing_deps", srv.ID, "", map[string]any{"mod_ids": ids})
		if err != nil {
			return nil, operatorOutput{}, err
		}
		return nil, operatorOutput{
			Server: serverRef(srv),
			Result: result,
			Note: "Each entry is one requested id. `found: true` carries the project_id and version_id to pass to " +
				"install_mod, already pinned to a build that fits this server. `found: false` means no compatible " +
				"project exists — report that rather than substituting a different mod.",
		}, nil
	}
}

func (svc *Service) installMod(p *Principal) mcp.ToolHandlerFor[installModInput, operatorOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in installModInput) (*mcp.CallToolResult, operatorOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeModsInstall, store.ServerPermissionModsInstall, false)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		source, err := operatorSource(in.Source)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		if !validOperatorID(in.ProjectID) || (in.VersionID != "" && !validOperatorID(in.VersionID)) {
			return nil, operatorOutput{}, toolError("invalid project or version id")
		}
		args := map[string]any{"source": source, "project_id": in.ProjectID, "version_id": in.VersionID, "with_deps": in.WithDependencies}
		result, err := svc.invokeOperator(ctx, p, "mods.install", srv.ID, "", args)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		return nil, operatorOutput{Server: serverRef(srv), Result: result, Note: "Restart and inspect diagnostics/logs before making another change."}, nil
	}
}

func (svc *Service) updateMod(p *Principal) mcp.ToolHandlerFor[updateModInput, operatorOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in updateModInput) (*mcp.CallToolResult, operatorOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeModsUpdate, store.ServerPermissionModsUpdate, false)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		if !validOperatorID(in.ModID) || (in.VersionID != "" && !validOperatorID(in.VersionID)) {
			return nil, operatorOutput{}, toolError("invalid mod or version id")
		}
		result, err := svc.invokeOperator(ctx, p, "mods.update", srv.ID, in.ModID, map[string]any{"version_id": in.VersionID})
		if err != nil {
			return nil, operatorOutput{}, err
		}
		return nil, operatorOutput{Server: serverRef(srv), Result: result, Note: "Restart and inspect diagnostics/logs before making another change."}, nil
	}
}

func (svc *Service) setModEnabled(p *Principal) mcp.ToolHandlerFor[setModEnabledInput, operatorOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in setModEnabledInput) (*mcp.CallToolResult, operatorOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeModsUpdate, store.ServerPermissionModsUpdate, false)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		if !validOperatorID(in.ModID) {
			return nil, operatorOutput{}, toolError("invalid mod id")
		}
		args := map[string]any{"enabled": in.Enabled, "force": in.AcknowledgeDependencyImpact, "disable_dependents": in.DisableDependents}
		result, err := svc.invokeOperator(ctx, p, "mods.enabled", srv.ID, in.ModID, args)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		return nil, operatorOutput{Server: serverRef(srv), Result: result, Note: "Restart and inspect diagnostics/logs before making another change."}, nil
	}
}

func (svc *Service) removeMod(p *Principal) mcp.ToolHandlerFor[removeModInput, operatorOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in removeModInput) (*mcp.CallToolResult, operatorOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeModsRemove, store.ServerPermissionModsRemove, false)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		if !validOperatorID(in.ModID) {
			return nil, operatorOutput{}, toolError("invalid mod id")
		}
		args := map[string]any{"force": in.AcknowledgeDependencyImpact, "disable_dependents": in.DisableDependents}
		result, err := svc.invokeOperator(ctx, p, "mods.remove", srv.ID, in.ModID, args)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		return nil, operatorOutput{Server: serverRef(srv), Result: result, Note: "Restart and inspect diagnostics/logs before making another change."}, nil
	}
}

func (svc *Service) listServerBackups(p *Principal) mcp.ToolHandlerFor[serverIDInput, operatorOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in serverIDInput) (*mcp.CallToolResult, operatorOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeBackupsCreate, store.ServerPermissionBackupsCreate, false)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		result, err := svc.invokeOperator(ctx, p, "backups.list", srv.ID, "", nil)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		return nil, operatorOutput{Server: serverRef(srv), Result: result, Note: "This is status-only. Never infer permission to create another backup from an existing backup record."}, nil
	}
}

func (svc *Service) createServerBackup(p *Principal) mcp.ToolHandlerFor[createBackupInput, operatorOutput] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in createBackupInput) (*mcp.CallToolResult, operatorOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeBackupsCreate, store.ServerPermissionBackupsCreate, false)
		if err != nil {
			return nil, operatorOutput{}, err
		}

		if req == nil || req.Params == nil || len(req.Params.InputResponses) == 0 {
			// Mint the state only now, when there is a question to attach it to.
			state, err := svc.store.CreateMCPBackupConfirmation(ctx, p.GrantID, srv.ID)
			if err != nil {
				slog.Error("mcp backup: issuing confirmation failed", "grant_id", p.GrantID, "error", err)
				return nil, operatorOutput{}, toolError("the backup confirmation could not be prepared")
			}
			return &mcp.CallToolResult{
				InputRequests: mcp.InputRequestMap{
					backupConfirmationID: &mcp.ElicitParams{
						Mode:    "form",
						Message: fmt.Sprintf("Create a full backup of %q? Backups can take a long time and consume substantial disk space. No backup will start unless you confirm here.", srv.Name),
						RequestedSchema: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"confirmed": map[string]any{
									"type":        "boolean",
									"description": "Confirm that you want to start this full backup now",
								},
							},
							"required": []string{"confirmed"},
						},
					},
				},
				RequestState: state,
			}, operatorOutput{}, nil
		}
		if len(req.Params.InputResponses) != 1 {
			return nil, operatorOutput{}, toolError("backup confirmation is invalid or belongs to another request; no backup was started")
		}
		response, ok := req.Params.InputResponses[backupConfirmationID].(*mcp.ElicitResult)
		if !ok || response == nil || response.Action != "accept" {
			// A refusal deliberately leaves the state unspent, so an operator who
			// declines by accident can be asked again without a fresh round trip.
			return nil, operatorOutput{}, toolError("backup was not confirmed; no backup was started")
		}
		confirmed, ok := response.Content["confirmed"].(bool)
		if !ok || !confirmed {
			return nil, operatorOutput{}, toolError("backup was not confirmed; no backup was started")
		}
		// Redeem last, immediately before acting: this is the step that makes one
		// human answer authorize one backup rather than every backup after it.
		if err := svc.store.ConsumeMCPBackupConfirmation(ctx, req.Params.RequestState, p.GrantID, srv.ID); err != nil {
			return nil, operatorOutput{}, toolError("backup confirmation is invalid, expired, or already used; no backup was started")
		}

		result, err := svc.invokeOperator(ctx, p, "backups.create", srv.ID, "", nil)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		svc.auditOperator(ctx, p, srv.ID, "backup.user_confirmed", map[string]any{"method": "mcp_elicitation"})
		return nil, operatorOutput{Server: serverRef(srv), Result: result, Note: "Backup creation continues asynchronously; the returned status starts as running."}, nil
	}
}

func (svc *Service) invokeOperator(ctx context.Context, p *Principal, operation, serverID, modID string, args map[string]any) (string, error) {
	if svc.operator == nil {
		return "", toolError("operator tools are unavailable")
	}
	result, err := svc.operator.Invoke(ctx, OperatorActor{UserID: p.UserID, GrantID: p.GrantID, ClientName: cleanName(p.ClientName), IP: p.IP}, operation, serverID, modID, args)
	if err != nil {
		return "", toolError("%s", clean(err.Error(), 240))
	}
	encoded, err := sanitizedJSON(result)
	if err != nil {
		return "", toolError("operator result unavailable")
	}
	return encoded, nil
}

// sanitizedJSON renders a value as a structured tool result, with every string
// inside it cleaned and every collection bounded.
//
// The decode step before sanitizing is the load-bearing part.
// sanitizeOperatorResult only recognises strings, slices, and maps; handed a
// typed Go struct it matches the `default` branch and returns it untouched. So
// a tool that marshalled a store record directly reached a model with no
// redaction, no invisible-character stripping, and no truncation — which is
// how raw node error text and mod-authored names got out through
// get_server_version_upgrade. Round-tripping through `any` first means there
// is one way to render a structured result and it is always the sanitized one.
func sanitizedJSON(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(sanitizeOperatorResult(decoded, 0))
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func (svc *Service) auditOperator(ctx context.Context, p *Principal, serverID, action string, detail any) {
	_ = svc.store.LogActionWithActor(ctx, p.UserID, "", p.GrantID, serverID, action, p.IP, map[string]any{"client_name": cleanName(p.ClientName), "operation": detail})
}

func validOperatorID(value string) bool { return safeOperatorID.MatchString(value) }

func operatorSource(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "modrinth":
		return "modrinth", nil
	case "curseforge", "hangar", "spigotmc":
		return strings.ToLower(strings.TrimSpace(value)), nil
	default:
		return "", toolError("unsupported mod source")
	}
}

func sanitizeOperatorResult(value any, depth int) any {
	if depth > 6 {
		return "[omitted]"
	}
	switch v := value.(type) {
	case string:
		return clean(v, maxEvidenceChars)
	case []any:
		if len(v) > 100 {
			v = v[:100]
		}
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, sanitizeOperatorResult(item, depth+1))
		}
		return out
	case map[string]any:
		out := make(map[string]any, min(len(v), 100))
		count := 0
		for key, item := range v {
			if count >= 100 {
				break
			}
			out[clean(key, 80)] = sanitizeOperatorResult(item, depth+1)
			count++
		}
		return out
	default:
		return v
	}
}
