# Server Permissions Plan

Goal: turn the currently unused `server_permissions` table into a real per-server collaboration system. Keep this as an implementation plan only; do not batch unrelated cleanup into the feature.

## 1. Current State

- `users.role` supports `admin`, `operator`, and `user`, but only `admin` has behavior.
- `operator` is currently only a label. Nothing in the router, middleware, or store grants it extra access.
- `servers.owner_id` is the only non-admin server access mechanism.
- `apps/api/internal/store/db.go`:
  - `ListServersForUser` returns only `servers.owner_id = ?`.
  - `UserCanAccessServer` checks only `servers.owner_id = ?`.
- `apps/api/internal/api/server_access.go` grants global admins all access, then calls `UserCanAccessServer`.
- `apps/api/internal/api/router.go` applies one blanket `requireServerAccess` middleware to every `/servers/{id}` route.
- `apps/api/migrations/001_initial.sql` already has:

```sql
CREATE TABLE server_permissions (
    server_id TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    permissions TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (server_id, user_id)
);
```

This table has no store methods, no handlers, no UI, and no route checks yet.

## 2. Desired V1 Behavior

Use fixed per-server permissions, stored as a JSON string array in `server_permissions.permissions`.

Recommended enum:

- `view` - server appears in lists; can read dashboard/status/log/audit basics.
- `power` - start, stop, restart, kill.
- `console` - open console and send console commands.
- `players` - list player details and run player actions such as kick, ban, op, whitelist.
- `files` - read, upload, edit, delete, rename server files and worlds.
- `mods` - install, update, enable, disable, uninstall mods and modpacks.
- `backups` - create, restore, delete backups and manage backup targets.
- `tasks` - create, update, delete scheduled tasks.
- `settings` - update server settings/options and reinstall runtime for version/platform changes.
- `admin` - manage this server's members and delete the server.

Rules:

- Global `admin` users bypass all per-server permission checks.
- The server `owner_id` implicitly has all per-server permissions.
- A row in `server_permissions` grants access only to the listed permissions.
- `view` should be required for a collaborator to see or open a server.
- `admin` implies every per-server permission for that server.
- Do not make the global `operator` role special in this pass. Either leave it as-is or document it as reserved.
- Do not build groups/teams in V1. Leave a schema-compatible path for them later.
- Revocation should be meaningful for long-lived sessions:
  - Console WebSockets are high-risk because they can execute server commands. Check `console` permission before forwarding each browser-to-agent command/message and also on a short periodic timer such as 5 seconds; close when access is lost.
  - Metrics WebSockets are lower-risk. Check `view` permission on connect and periodically, such as every 30 seconds; close when access is lost.
- Per-server admins may manage other per-server members, but owner immutability is enforced server-side. Decide and test whether a per-server admin can remove their own `admin`; recommended V1 rule is to allow self-removal only when the server owner still exists and the request is not trying to alter the immutable owner record.
- A server may have zero explicit `server_permissions` rows with `admin`; that is valid because the owner always has implicit server admin. Do not build backend or UI assumptions that require an explicit per-server admin member row.

Security note: `console` is powerful. A user who can send arbitrary Minecraft commands can often grant themselves in-game privileges. Keep `players` separate so a trusted helper can whitelist/kick/ban without full console access.

## 3. Database Plan

Use the existing table. Add only a targeted index for lookup by user:

`apps/api/migrations/009_server_permissions_user_index.sql`

```sql
-- +goose Up
CREATE INDEX server_permissions_user_id ON server_permissions(user_id);

-- +goose Down
DROP INDEX IF EXISTS server_permissions_user_id;
```

No new columns are required for V1. If auditability becomes important later, add `created_at`, `updated_at`, and `granted_by` in a separate migration.

Index notes:

- The existing primary key `(server_id, user_id)` covers list-members-by-server queries because `server_id` is the leftmost key.
- The new `server_permissions_user_id` index is for user-scoped lookups such as "which servers are shared with this user".

Validation note:

- SQLite will not enforce the permission enum inside the JSON array. V1 accepts app-layer validation only, but this is a known gap. Every write path must go through `NormalizeServerPermissions`; do not write raw permission JSON from handlers or migrations unless it has the same validation.

## 4. Store Plan

Add model/types near the existing `User` and `Server` structs in `apps/api/internal/store/db.go`.

Suggested Go shapes:

```go
type ServerPermission string

const (
    ServerPermissionView     ServerPermission = "view"
    ServerPermissionPower    ServerPermission = "power"
    ServerPermissionConsole  ServerPermission = "console"
    ServerPermissionPlayers  ServerPermission = "players"
    ServerPermissionFiles    ServerPermission = "files"
    ServerPermissionMods     ServerPermission = "mods"
    ServerPermissionBackups  ServerPermission = "backups"
    ServerPermissionTasks    ServerPermission = "tasks"
    ServerPermissionSettings ServerPermission = "settings"
    ServerPermissionAdmin    ServerPermission = "admin"
)

type ServerMember struct {
    ServerID    string   `json:"server_id"`
    UserID      string   `json:"user_id"`
    Email       string   `json:"email"`
    DisplayName *string  `json:"display_name"`
    Role        string   `json:"role"`
    Permissions []string `json:"permissions"`
}
```

Add helpers:

- `NormalizeServerPermissions([]string) ([]string, error)`
  - validate enum values.
  - de-dupe.
  - sort for stable output.
  - if `admin` is present, either store all permissions or store `admin` only and expand in authorization. Prefer store exactly what the user selected and expand in authorization.
- `HasServerPermission(perms []string, needed ServerPermission) bool`
  - true when `needed` exists or `admin` exists.
- email lookup helper for member management:
  - trim and lowercase lookup input.
  - query case-insensitively, e.g. `LOWER(email) = LOWER(?)`, unless the app first normalizes all stored emails.
  - if duplicate case variants somehow exist, fail with a conflict instead of picking one silently.

Add store methods:

- `ListServerMembers(ctx, serverID string) ([]*ServerMember, error)`
  - join `server_permissions` to `users`.
  - exclude the owner row if one somehow exists, or mark owner separately in handler response.
- `GetServerMember(ctx, serverID, userID string) (*ServerMember, error)`
- `SetServerPermissions(ctx, serverID, userID string, perms []string) error`
  - `INSERT ... ON CONFLICT(server_id, user_id) DO UPDATE SET permissions=excluded.permissions`.
- `DeleteServerPermissions(ctx, serverID, userID string) error`
- `GetServerPermissions(ctx, serverID, userID string) ([]string, bool, error)`
  - returns permissions and whether a row exists.
- `UserHasServerPermission(ctx, userID, serverID string, needed ServerPermission) (bool, error)`
  - true for `owner_id`.
  - otherwise read `server_permissions` and apply `HasServerPermission`.

Update existing methods:

- `UserCanAccessServer` should become owner-or-member:
  - true if server owner.
  - true if `server_permissions` row exists with `view` or `admin`.
- `ListServersForUser` should return owned OR shared servers:
  - include `servers.owner_id = ?`.
  - include `server_permissions.user_id = ?` with `view`/`admin`.
  - avoid duplicates with `DISTINCT` or a union.
  - keep `ORDER BY name`.

## 5. API/Middleware Plan

Replace the one-size `requireServerAccess` model with permission-aware middleware.

Suggested API:

```go
func requireServerPermission(s *store.Store, permission store.ServerPermission) func(http.Handler) http.Handler
```

Behavior:

- 401 if no claims.
- allow if claims role is global `admin`.
- allow if `UserHasServerPermission` says yes.
- 403 otherwise.

Keep `requireServerAccess` as a thin wrapper for `view` if that reduces churn:

```go
func requireServerAccess(s *store.Store) func(http.Handler) http.Handler {
    return requireServerPermission(s, store.ServerPermissionView)
}
```

Route mapping in `apps/api/internal/api/router.go`:

- `view`:
  - `GET /servers/{id}`
  - `GET /servers/{id}/status`
  - `GET /servers/{id}/metrics`
  - `GET /servers/{id}/audit`
  - `GET /servers/{id}/log-events`
- `power`:
  - `POST /servers/{id}/start`
  - `POST /servers/{id}/stop`
  - `POST /servers/{id}/restart`
  - `POST /servers/{id}/kill`
- `console`:
  - `GET /servers/{id}/console`
  - `POST /servers/{id}/command`
- `players`:
  - `GET /servers/{id}/players`
  - `GET /servers/{id}/players/meta`
  - `GET /servers/{id}/players/{uuid}`
  - `POST /servers/{id}/players/action`
- `files`:
  - every `/files` endpoint.
  - `GET /servers/{id}/files/download`
  - upload/read/write/delete/rename/mkdir/tree/content.
- `mods`:
  - every `/mods` endpoint, including sources, categories, search, project/version reads, install/upload/custom upload, conflicts, modpacks, update runs, skipped versions, pin/enabled/update/uninstall.
- `backups`:
  - every `/backups` and `/backup-targets` endpoint.
- `tasks`:
  - every `/tasks` endpoint.
- `settings`:
  - `PUT /servers/{id}`
  - `POST /servers/{id}/reinstall`
- `admin`:
  - `DELETE /servers/{id}`
  - member-management endpoints.

Important: do not leave a parent `r.Use(requireServerAccess(s))` around the whole `/servers/{id}` group if it accidentally lets every collaborator with only `view` reach mutation routes. Apply checks per route or per nested group.

Fail-closed route migration rule:

- Before editing `router.go`, make an exhaustive route-to-permission table from the current `/servers/{id}` route list.
- During implementation, every route under `/servers/{id}` must be wrapped by exactly one permission requirement or by an intentionally stricter parent group.
- Add a table-driven router test that exercises representative routes from every permission group and proves `view` alone cannot reach mutations.
- When adding future `/servers/{id}` routes, require the developer to choose a permission at the route declaration site.

WebSocket revocation:

- `GET /servers/{id}/console` requires `console`, not just `view`.
- `GET /servers/{id}/metrics` requires `view`.
- Both handlers should re-check the relevant permission while the connection is open, at a modest interval such as 30 seconds, and close the WebSocket when permission is revoked or the user is deleted.
- If active disconnect-on-revoke is too expensive for V1, the periodic re-check is the minimum acceptable behavior.

## 6. Member Management API

Add a new handler file:

`apps/api/internal/api/handlers/server_members.go`

Proposed endpoints under `/servers/{id}`:

- `GET /members`
  - requires per-server `admin`.
  - returns owner plus member rows, or returns owner separately.
- `POST /members`
  - requires per-server `admin`.
  - body: `{ "user_id": "...", "email": "...", "permissions": ["view", "players"] }`.
  - accept either `user_id` or exact `email`; exact email is useful so non-global server admins do not need access to the global users list.
  - normalize email lookup input. Return a generic "user not found or cannot be added" style error so per-server admins do not get a clean account-enumeration oracle.
  - reject missing user, owner user, unknown permissions, and empty permission arrays.
- `PUT /members/{userId}`
  - requires per-server `admin`.
  - full-replace semantics, not merge/patch.
  - body: `{ "permissions": [...], "expected_permissions": [...] }`.
  - `expected_permissions` should be the normalized set the UI last loaded. If the current DB value differs, return `409 Conflict` so two server admins do not silently clobber each other's changes.
  - reject owner user and unknown permissions.
  - if the target user is the caller and this removes their own `admin`, allow only if this is an intentional self-demotion case; never allow modifying the immutable owner record.
- `DELETE /members/{userId}`
  - requires per-server `admin`.
  - reject owner user; deleting a non-member should be idempotent 204.
  - if the target user is the caller, treat it as an intentional leave-server action and ensure the response does not leave the UI assuming they still have access.
- `GET /members/me`
  - requires `view`.
  - returns `{ "owner": bool, "global_admin": bool, "permissions": [...] }`.
  - lets the frontend hide tabs/actions without probing every endpoint.

Audit:

- `server.member.add`
- `server.member.update`
- `server.member.remove`

Audit detail should include target user id/email and before/after permission arrays.

Validation and side-effect ordering:

- Validate target user, owner checks, permission enum, and expected/current permission match before writing audit rows or membership rows.
- Failed member-add attempts should return a generic error and should not produce success-shaped side effects that make account enumeration easier.

## 7. Frontend Plan

Types in `apps/web/src/lib/types.ts`:

```ts
export type ServerPermission =
  | "view"
  | "power"
  | "console"
  | "players"
  | "files"
  | "mods"
  | "backups"
  | "tasks"
  | "settings"
  | "admin";

export interface ServerMember {
  server_id: string;
  user_id: string;
  email: string;
  display_name: string | null;
  role: User["role"];
  permissions: ServerPermission[];
}

export interface MyServerPermissions {
  owner: boolean;
  global_admin: boolean;
  permissions: ServerPermission[];
}
```

API wrappers in `apps/web/src/lib/api.ts` under `api.servers`:

- `members(id)`
- `myPermissions(id)`
- `addMember(id, body)`
- `updateMember(id, userId, permissions)`
- `removeMember(id, userId)`

Server detail UI:

- Add `access` to `ServerSection` in `apps/web/src/components/servers/shared.tsx`, likely group `Manage`.
- Add `apps/web/src/components/servers/access-tab.tsx`.
- Render it from `apps/web/src/routes/servers/$id.tsx`.

Access tab behavior:

- Show owner as immutable.
- Show existing members with permission checkboxes/toggles.
- Add member by exact email or user id.
- Use clear permission labels:
  - View
  - Power controls
  - Console commands
  - Players and whitelist
  - Files and worlds
  - Mods and updates
  - Backups
  - Scheduled tasks
  - Server settings
  - Manage access
- If `admin` is selected, visually indicate it grants all server permissions.
- Keep global role badges visible, but clarify through labels only if needed: global `operator` is not the same as server permissions.
- Add a small admin-facing note wherever global roles are edited, likely the Users page: global `operator` is reserved today and does not grant per-server access by itself. Per-server access is managed from each server's Access tab.
- Do not assume the member list contains an explicit admin row. The owner row is the durable authority for server administration.

Permission-aware UI:

- Query `api.servers.myPermissions(id)` on the server detail page.
- Hide or disable tabs/actions when the user lacks the matching permission.
- For member updates, send full replacement permission arrays with the row's last loaded `expected_permissions`; on `409`, refetch members and ask the user to retry.
- Do not rely on UI gating for security; backend checks are authoritative.
- Minimum acceptable first pass: backend enforcement plus Access tab. UI hiding can be basic as long as forbidden actions return good errors.

## 8. Tests

Backend tests first.

Store tests:

- `SetServerPermissions` creates and updates rows.
- invalid permissions are rejected by normalization before store write.
- `DeleteServerPermissions` revokes access.
- `UserCanAccessServer` allows owner and collaborator with `view`, denies collaborator without `view`.
- `UserHasServerPermission` allows owner, `admin` permission, and exact permission; denies missing permission.
- `ListServersForUser` includes owned and shared servers without duplicates.

Middleware/router tests:

- global admin can access every route.
- owner can access every per-server route.
- collaborator with only `view` can `GET /servers/{id}` but cannot start/stop/delete.
- collaborator with `power` can start/stop but cannot files/mods.
- collaborator with `players` can whitelist/kick via `players/action` but cannot console command.
- collaborator with `admin` can manage members and delete server.
- no row returns 403.
- console WebSocket stops forwarding browser-to-agent messages after permission is revoked and closes on the short re-check.
- metrics WebSocket closes after `view` permission is revoked on its periodic re-check.
- an explicit route mapping test covers every current `/servers/{id}` route group, so unmapped routes do not silently become public or `view`-only.

Handler tests:

- add member by email.
- add member by mixed-case email resolves consistently or fails with a clear conflict if stored duplicates exist.
- add member by missing email does not leak more detail than necessary.
- add/update rejects unknown permissions.
- add/update rejects owner as explicit member.
- update with stale `expected_permissions` returns `409 Conflict` and leaves the DB unchanged.
- update/delete handles caller self-removal intentionally and never modifies owner access.
- member list includes user identity and permissions.
- remove member revokes access.
- audit rows are created for add/update/remove.
- failed validation paths do not write audit rows that reveal whether a target email exists.

Frontend:

- No existing Vitest setup is required for this feature.
- Run `pnpm build` from `apps/web`.
- Manually smoke the Access tab with one admin, one owner, one helper account.

## 9. Implementation Order

1. Add migration `009_server_permissions_user_index.sql`.
2. Add permission enum, normalization, and store methods.
3. Update `UserCanAccessServer` and `ListServersForUser`.
4. Add unit tests for store behavior.
5. Replace route access with `requireServerPermission`.
6. Add member-management handlers and routes.
7. Add middleware/handler tests.
8. Add frontend types and API wrappers.
9. Add `access-tab.tsx` and the `access` server section.
10. Add basic UI gating from `members/me`.
11. Run:
    - `go test ./...` in `apps/api`.
    - `pnpm build` in `apps/web`.
12. Update `PLAN_FOR_CODEX.md` only after implementation lands, moving the server permissions item to the changelog.

## 10. Explicit Non-Goals For V1

- No groups/teams.
- No reusable permission templates.
- No path-level file permissions.
- No console read-only vs console send split.
- No database ownership transfer UI.
- No change to global `operator` behavior.

These can be added later without discarding this design. Groups would layer on as an additional source of effective permissions, merged with direct `server_permissions` rows.
