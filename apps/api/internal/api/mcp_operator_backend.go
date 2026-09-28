package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/mcsm/api/internal/api/handlers"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/mcpserver"
)

const maxMCPOperatorResponseBytes = 512 << 10

type mcpOperatorBackend struct {
	mods    *handlers.ModHandlers
	backups *handlers.BackupHandlers
	players *handlers.PlayersHandlers
	servers *handlers.ServerHandlers
	files   *handlers.FileHandlers
	tasks   *handlers.TaskHandlers
	mc      *handlers.MinecraftHandlers
}

func newMCPOperatorBackend(mods *handlers.ModHandlers, backups *handlers.BackupHandlers, players *handlers.PlayersHandlers,
	servers *handlers.ServerHandlers, files *handlers.FileHandlers, tasks *handlers.TaskHandlers,
	mc *handlers.MinecraftHandlers) mcpserver.OperatorBackend {
	return &mcpOperatorBackend{mods: mods, backups: backups, players: players, servers: servers,
		files: files, tasks: tasks, mc: mc}
}

// Invoke adapts a closed operation vocabulary to the same application handlers
// used by the dashboard. MCP authorization has already intersected grant,
// server allowlist, and live user RBAC; the handlers remain the single owner of
// mod compatibility, dependency, source, node, and backup behavior.
func (b *mcpOperatorBackend) Invoke(ctx context.Context, actor mcpserver.OperatorActor, operation, serverID, modID string, args map[string]any) (any, error) {
	if b == nil || b.mods == nil || b.backups == nil || b.players == nil || b.servers == nil ||
		b.files == nil || b.tasks == nil || b.mc == nil {
		return nil, errors.New("operator backend unavailable")
	}

	method := http.MethodGet
	path := "/internal"
	var body any
	var handler http.HandlerFunc
	params := map[string]string{"id": serverID}

	switch operation {
	case "mods.list":
		handler = b.mods.ListReadOnly
	case "mods.search":
		method, body, handler = http.MethodPost, args, b.mods.Search
	case "mods.updates":
		handler = b.mods.Updates
	case "mods.resolve_missing_deps":
		// The same read-only resolution the dashboard's missing-dependency dialog
		// runs, including the loader-id aliases (a jar declaring "fabric" is
		// published as "fabric-api"), so an agent resolves a blocker the way the
		// panel would rather than guessing at a search term.
		method, body, handler = http.MethodPost, args, b.mods.ResolveMissingDeps
	case "mods.version_check":
		handler = b.mods.VersionCheck
		q := url.Values{}
		if target, _ := args["mc_version"].(string); target != "" {
			q.Set("mc_version", target)
		}
		path += "?" + q.Encode()
	case "mods.install":
		method, body, handler = http.MethodPost, args, b.mods.Install
	case "mods.update":
		method, body, handler = http.MethodPost, args, b.mods.Update
		params["modId"] = modID
	case "mods.enabled":
		method, body, handler = http.MethodPost, args, b.mods.SetEnabled
		params["modId"] = modID
	case "mods.remove":
		method, handler = http.MethodDelete, b.mods.Uninstall
		params["modId"] = modID
		q := url.Values{}
		if boolArg(args, "force") {
			q.Set("force", "1")
		}
		if boolArg(args, "disable_dependents") {
			q.Set("disable_dependents", "1")
		}
		path += "?" + q.Encode()
	case "players.action":
		// ActionDelegated, not Action: MCP authorization has already intersected
		// grant, allowlist, and the caller's live players.whitelist permission,
		// and Action re-derives that from HTTP claims this request does not have.
		//
		// Because it re-derives nothing, it also refuses nothing — the action
		// set it accepts includes op, ban, and kick. The facade only ever sends
		// the two whitelist actions, so that is what this adapter forwards, and
		// the check is here for the same reason files.read gets a second path
		// check: this is the last place that can still say no.
		if !mcpserver.IsDelegatedPlayerAction(stringArg(args, "action")) {
			return nil, errors.New("unsupported operator operation")
		}
		method, body, handler = http.MethodPost, args, b.players.ActionDelegated
	case "console.command":
		// The command string here is rendered by mcpserver's console verb table
		// from a validated verb and arguments. It never originates with the
		// model, and this adapter must not become a way to pass one through.
		command, _ := args["command"].(string)
		if command == "" {
			return nil, errors.New("console command was not rendered")
		}
		method, body, handler = http.MethodPost, map[string]any{"command": command}, b.servers.Command
	case "files.read":
		// The path is a constant chosen by the facade, never a tool argument —
		// see mcpserver/inspect_tools.go. It is checked here as well, because a
		// tool added later is the thing that would widen it, and this adapter is
		// the last place that can still say no.
		read := stringArg(args, "path")
		if !mcpserver.IsReadablePath(read) {
			return nil, errors.New("unsupported operator operation")
		}
		handler = b.files.GetContent
		q := url.Values{"path": {read}}
		if tail := stringArg(args, "tail_bytes"); tail != "" {
			q.Set("tail_bytes", tail)
		}
		path += "?" + q.Encode()
	case "files.tree":
		dir := stringArg(args, "path")
		if !mcpserver.IsReadablePath(dir) {
			return nil, errors.New("unsupported operator operation")
		}
		handler = b.files.Tree
		path += "?" + url.Values{"path": {dir}, "depth": {"1"}}.Encode()
	case "java.list":
		handler = b.servers.JavaInstallations
	case "players.list":
		handler = b.players.List
	case "players.meta":
		handler = b.players.Meta
	case "players.bans":
		handler = b.players.Bans
	case "tasks.list":
		handler = b.tasks.List
	case "stats.availability":
		handler = b.servers.Stats
		path += "?" + url.Values{"days": {stringArg(args, "days")}}.Encode()
	case "mods.update_runs":
		handler = b.mods.ListUpdateRuns
	case "mods.skipped_versions":
		handler = b.mods.ListSkippedVersions
	case "minecraft.versions":
		handler = b.mc.Versions
	case "minecraft.loaders":
		handler = b.mc.LoaderVersions
		path += "?" + url.Values{"loader": {stringArg(args, "loader")}, "mc_version": {stringArg(args, "mc_version")}}.Encode()
	case "backups.list":
		handler = b.backups.ListBackupsSafe
	case "backups.create":
		method, handler = http.MethodPost, b.backups.CreateBackup
	default:
		return nil, errors.New("unsupported operator operation")
	}

	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, errors.New("could not encode operation")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://mcsm.internal"+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, errors.New("could not build operation")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	routeCtx := chi.NewRouteContext()
	for key, value := range params {
		routeCtx.URLParams.Add(key, value)
	}
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeCtx))
	req = req.WithContext(auth.WithDelegatedActor(req.Context(), &auth.DelegatedActor{
		UserID: actor.UserID, GrantID: actor.GrantID, ClientName: actor.ClientName, IP: actor.IP,
	}))

	rec := httptest.NewRecorder()
	handler(rec, req)
	res := rec.Result()
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxMCPOperatorResponseBytes+1))
	if err != nil {
		return nil, errors.New("operation response unavailable")
	}
	if len(raw) > maxMCPOperatorResponseBytes {
		return nil, errors.New("operation response exceeded its safety limit")
	}
	if res.StatusCode >= 400 {
		slog.Warn("MCP operator handler rejected operation", "operation", operation, "status", res.StatusCode, "response", string(raw))
		// Existing HTTP handlers may include node transport URLs, local paths,
		// or upstream download details in their error strings. The MCP boundary
		// returns a stable application-authored error instead of relaying them.
		return nil, operatorResponseError(raw)
	}
	return decodeOperatorBody(res.Header.Get("Content-Type"), raw)
}

// decodeOperatorBody turns a handler's response into what the facade receives.
//
// The content-type branch is the load-bearing one. A handler that answers with
// text — file content is the only one — is returned as a string, because
// forcing it through a JSON decode is what made every raw log read fail with
// "operation returned an invalid response": the agent serves a log file as
// text/plain, and a log file is not JSON.
func decodeOperatorBody(contentType string, raw []byte) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{"status": "completed"}, nil
	}
	if contentType != "" && !strings.Contains(strings.ToLower(contentType), "json") {
		return string(raw), nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, errors.New("operation returned an invalid response")
	}
	return decoded, nil
}

func operatorResponseError(raw []byte) error {
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &body) == nil {
		switch body.Error {
		case "failed to register server directory":
			return errors.New("node agent unavailable")
		case "server not found", "node not found", "mod not found", "mod has no source project",
			"no files in version", "source does not permit downloading this file":
			return errors.New(body.Error)
		}
	}
	return errors.New("operator operation failed")
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func boolArg(args map[string]any, key string) bool {
	value, _ := args[key].(bool)
	return value
}
