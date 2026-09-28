package mcpserver

import (
	"context"
	"strings"

	"github.com/mcsm/api/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type versionUpgradeInput struct {
	ServerID      string `json:"server_id" jsonschema:"a server id from list_servers"`
	TargetVersion string `json:"target_version" jsonschema:"the exact target Minecraft version"`
	Reason        string `json:"reason,omitempty" jsonschema:"specific justification shown to the approving human"`
}

type versionUpgradeStatusInput struct {
	ServerID string `json:"server_id"`
	RunID    string `json:"run_id"`
}

func (svc *Service) planVersionUpgrade(p *Principal) mcp.ToolHandlerFor[versionUpgradeInput, operatorOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in versionUpgradeInput) (*mcp.CallToolResult, operatorOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeModsRead, store.ServerPermissionSettings, false)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		target := strings.TrimSpace(in.TargetVersion)
		if _, ok := store.MCPUpgradeAction(target); !ok || target == srv.MCVersion {
			return nil, operatorOutput{}, toolError("invalid or unchanged target version")
		}
		result, err := svc.invokeOperator(ctx, p, "mods.version_check", srv.ID, "", map[string]any{"mc_version": target})
		if err != nil {
			return nil, operatorOutput{}, err
		}
		return nil, operatorOutput{Server: serverRef(srv), Result: result, Note: "Read-only plan. Starting this upgrade requires a separate approval in ServerManager — password/MFA unless the owner enabled automatic upgrade approval — and will create one full restore-point backup."}, nil
	}
}

func (svc *Service) requestVersionUpgrade(p *Principal) mcp.ToolHandlerFor[versionUpgradeInput, actionRequestOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in versionUpgradeInput) (*mcp.CallToolResult, actionRequestOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeActionsRequest, store.ServerPermissionSettings, false)
		if err != nil {
			return nil, actionRequestOutput{}, err
		}
		action, ok := store.MCPUpgradeAction(in.TargetVersion)
		if !ok || strings.TrimSpace(in.TargetVersion) == srv.MCVersion {
			return nil, actionRequestOutput{}, toolError("invalid or unchanged target version")
		}
		req, err := svc.store.CreateMCPActionRequest(ctx, p.GrantID, p.UserID, srv.ID, action, in.Reason)
		if err != nil {
			return nil, actionRequestOutput{}, toolError("the upgrade request could not be filed right now")
		}
		svc.audit(ctx, p, srv.ID, "mcp.upgrade.requested", map[string]any{"request_id": req.ID, "target_version": strings.TrimSpace(in.TargetVersion), "client": cleanName(p.ClientName)})
		// Gated on the owner's separate upgrade toggle, never on the lifecycle
		// one: this reinstalls the runtime, rewrites every managed mod, and can
		// roll the world back to a restore point.
		req = svc.autoApproveIfPolicyAllows(ctx, p, req)
		svc.notifyActionFiled(ctx, p, req, srv)
		return nil, actionOutput(req, srv, svc.now()), nil
	}
}

func (svc *Service) getVersionUpgrade(p *Principal) mcp.ToolHandlerFor[versionUpgradeStatusInput, operatorOutput] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in versionUpgradeStatusInput) (*mcp.CallToolResult, operatorOutput, error) {
		srv, err := svc.authorize(ctx, p, in.ServerID, store.MCPScopeActionsRequest, store.ServerPermissionSettings, false)
		if err != nil {
			return nil, operatorOutput{}, err
		}
		var run *store.VersionMigration
		if strings.TrimSpace(in.RunID) == "" {
			runs, listErr := svc.store.ListVersionMigrations(ctx, srv.ID, 1)
			if listErr != nil || len(runs) == 0 {
				return nil, operatorOutput{}, toolError("no upgrade runs for this server")
			}
			run = runs[0]
		} else {
			run, err = svc.store.GetVersionMigration(ctx, in.RunID)
			if err != nil || run.ServerID != srv.ID {
				return nil, operatorOutput{}, toolError("no such upgrade run")
			}
		}
		// Through the same sanitizer every other structured result goes through.
		// A migration record is not panel-authored prose: its per-mod `error`
		// and its `message` are raw Go error strings from the node and the
		// download path, and its mod names come from the jars themselves.
		result, err := sanitizedJSON(run)
		if err != nil {
			return nil, operatorOutput{}, toolError("upgrade status is unavailable right now")
		}
		return nil, operatorOutput{Server: serverRef(srv), Result: result, Note: "Poll until success, partial, reverted, or failed. Reverted means the pre-upgrade backup and database snapshot were restored."}, nil
	}
}
