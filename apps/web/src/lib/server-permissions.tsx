import { createContext, useContext, useMemo } from "react";
import type { MyServerPermissions, ServerPermission } from "./types";
import { can, deniedReason } from "./permissions";

// Every server-scoped view sits inside one of these. Without it, each panel,
// dialog and row that owns an action would have to be handed a `can` prop from
// the server detail page, and the ones nested three levels deep (a mod row
// inside a list inside a tab) would drift out of sync the first time someone
// added a button without threading the prop.

interface ServerPermissionsValue {
  permissions: MyServerPermissions | undefined;
  // False while the permissions request is still in flight. Controls render
  // inert but unexplained during this window — claiming "you don't have
  // permission" before we know would be wrong as often as it is right.
  loaded: boolean;
}

const ServerPermissionsContext = createContext<ServerPermissionsValue>({
  permissions: undefined,
  loaded: false,
});

export function ServerPermissionsProvider({
  permissions,
  children,
}: {
  permissions: MyServerPermissions | undefined;
  children: React.ReactNode;
}) {
  const value = useMemo<ServerPermissionsValue>(
    () => ({ permissions, loaded: permissions !== undefined }),
    [permissions],
  );
  return (
    <ServerPermissionsContext.Provider value={value}>
      {children}
    </ServerPermissionsContext.Provider>
  );
}

/** Wraps a bare permission list (as the server list endpoint returns it) in the
 *  shape the shared `can` helper expects. The API resolves owner/admin to the
 *  full set server-side, so no extra flags are needed here. */
export function listPermissions(
  perms: ServerPermission[] | undefined,
): MyServerPermissions | undefined {
  if (!perms) return undefined;
  return { owner: false, global_admin: false, permissions: perms };
}

/** `can(permission)` for the surrounding server. */
export function useCan(): (permission: ServerPermission) => boolean {
  const { permissions } = useContext(ServerPermissionsContext);
  return useMemo(
    () => (permission: ServerPermission) => can(permissions, permission),
    [permissions],
  );
}

export interface PermissionState {
  allowed: boolean;
  /** Permissions haven't arrived yet — gate the control, but stay silent. */
  pending: boolean;
  /** Empty when allowed or pending. */
  reason: string;
}

/** Resolves a permission against the surrounding server. Passing several means
 *  "any of these" — for a control that opens onto a choice of actions, like the
 *  add-player dialog, where holding any one of them makes it worth opening. */
export function usePermission(
  permission: ServerPermission | ServerPermission[],
): PermissionState {
  const { permissions, loaded } = useContext(ServerPermissionsContext);
  // A caller passing an array builds a fresh one each render, so key the memo
  // on the contents rather than the array's identity.
  const key = Array.isArray(permission) ? permission.join(",") : permission;
  return useMemo(() => {
    if (!loaded) return { allowed: false, pending: true, reason: "" };
    const needed = Array.isArray(permission) ? permission : [permission];
    const allowed = needed.some((p) => can(permissions, p));
    return {
      allowed,
      pending: false,
      reason: allowed ? "" : deniedReason(needed),
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [permissions, loaded, key]);
}
