import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, ShieldCheck, Trash2 } from "lucide-react";
import { api } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Dialog, ConfirmDialog } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useNotifications } from "@/store/notifications";
import type { ServerMember, ServerPermission } from "@/lib/types";
import {
  PERMISSION_MODEL,
  collapsePerms,
  permissionParent,
  samePerms,
} from "@/lib/permissions";

// A permission appears checked when it's selected directly, implied by its
// parent group, or implied by admin (which grants everything).
function isChecked(value: ServerPermission[], perm: ServerPermission): boolean {
  if (perm !== "admin" && value.includes("admin")) return true;
  if (value.includes(perm)) return true;
  const parent = permissionParent(perm);
  return parent ? value.includes(parent) : false;
}

// A permission is locked (checked but not editable) when something above it
// already grants it: admin for anything, or the parent group for a leaf.
function isImplied(value: ServerPermission[], perm: ServerPermission): boolean {
  if (perm !== "admin" && value.includes("admin")) return true;
  const parent = permissionParent(perm);
  return parent ? value.includes(parent) : false;
}

function toggleGroup(
  value: ServerPermission[],
  group: ServerPermission,
): ServerPermission[] {
  if (value.includes(group)) {
    return collapsePerms(value.filter((p) => p !== group));
  }
  // Selecting the whole group makes its individual leaves redundant.
  return collapsePerms([
    ...value.filter((p) => permissionParent(p) !== group),
    group,
  ]);
}

function toggleLeaf(
  value: ServerPermission[],
  leaf: ServerPermission,
): ServerPermission[] {
  return value.includes(leaf)
    ? collapsePerms(value.filter((p) => p !== leaf))
    : collapsePerms([...value, leaf]);
}

function PermissionGrid({
  value,
  onChange,
  disabled,
}: {
  value: ServerPermission[];
  onChange?: (next: ServerPermission[]) => void;
  disabled?: boolean;
}) {
  return (
    <div className="space-y-2">
      {PERMISSION_MODEL.map((g) => {
        const groupChecked = isChecked(value, g.group);
        const groupLocked = disabled || isImplied(value, g.group);
        return (
          <div
            key={g.group}
            className="rounded-md border border-border bg-surface-2 p-3"
          >
            <label
              className={`flex items-start gap-3 text-sm ${
                groupLocked ? "opacity-90" : "cursor-pointer"
              }`}
            >
              <input
                type="checkbox"
                className="mt-1 h-4 w-4 accent-accent"
                checked={groupChecked}
                disabled={groupLocked}
                onChange={() => onChange?.(toggleGroup(value, g.group))}
              />
              <span className="min-w-0">
                <span className="block font-medium text-text-primary">
                  {g.label}
                </span>
                <span className="block text-xs text-text-secondary">
                  {g.detail}
                </span>
              </span>
            </label>
            {g.leaves.length > 0 && (
              <div className="mt-2 flex flex-wrap gap-2 pl-7">
                {g.leaves.map((leaf) => {
                  const checked = isChecked(value, leaf.value);
                  const locked = disabled || isImplied(value, leaf.value);
                  return (
                    <label
                      key={leaf.value}
                      className={`flex items-center gap-1.5 rounded border px-2 py-1 text-xs ${
                        checked
                          ? "border-accent/40 bg-accent/10 text-text-primary"
                          : "border-border text-text-secondary"
                      } ${locked ? "opacity-70" : "cursor-pointer hover:border-accent/40"}`}
                    >
                      <input
                        type="checkbox"
                        className="h-3 w-3 accent-accent"
                        checked={checked}
                        disabled={locked}
                        onChange={() => onChange?.(toggleLeaf(value, leaf.value))}
                      />
                      {leaf.label}
                    </label>
                  );
                })}
              </div>
            )}
          </div>
        );
      })}
    </div>
  );
}

function MemberIdentity({ member }: { member: ServerMember }) {
  return (
    <div className="min-w-0">
      <div className="flex flex-wrap items-center gap-2">
        <p className="truncate text-sm font-medium text-text-primary">
          {member.display_name || member.email}
        </p>
        <span className="rounded border border-border bg-surface-2 px-2 py-0.5 text-[10px] uppercase tracking-wide text-text-secondary">
          {member.owner ? "Owner" : member.role}
        </span>
      </div>
      {member.display_name && (
        <p className="truncate text-xs text-text-secondary">{member.email}</p>
      )}
    </div>
  );
}

function MemberRow({
  member,
  onRemove,
}: {
  member: ServerMember;
  onRemove: (member: ServerMember) => void;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [draft, setDraft] = useState(member.permissions);

  useEffect(() => {
    setDraft(member.permissions);
  }, [member.permissions]);

  const update = useMutation({
    mutationFn: () =>
      api.servers.updateMember(
        member.server_id,
        member.user_id,
        collapsePerms(draft),
        member.permissions,
      ),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server-members", member.server_id] });
      qc.invalidateQueries({ queryKey: ["server-permissions", member.server_id] });
      success("Member updated");
    },
    onError: (e: Error) => {
      if (e.message.includes("changed")) {
        qc.invalidateQueries({ queryKey: ["server-members", member.server_id] });
      }
      error("Update failed", e.message);
    },
  });

  const changed = !samePerms(draft, member.permissions);
  const empty = collapsePerms(draft).length === 0;

  return (
    <div className="space-y-4 border-b border-border/60 px-4 py-4 last:border-b-0 sm:px-5">
      <div className="flex items-start justify-between gap-3">
        <MemberIdentity member={member} />
        <Button
          variant="ghost"
          size="icon"
          onClick={() => onRemove(member)}
          title="Remove member"
          aria-label="Remove member"
        >
          <Trash2 className="h-4 w-4 text-red-400" />
        </Button>
      </div>
      <PermissionGrid value={draft} onChange={setDraft} />
      <div className="flex justify-end gap-2">
        {changed && (
          <Button
            variant="outline"
            size="sm"
            onClick={() => setDraft(member.permissions)}
          >
            Reset
          </Button>
        )}
        <Button
          size="sm"
          disabled={!changed || empty}
          loading={update.isPending}
          onClick={() => update.mutate()}
        >
          Save
        </Button>
      </div>
    </div>
  );
}

function AddMemberDialog({
  serverId,
  open,
  onClose,
}: {
  serverId: string;
  open: boolean;
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const { success, error } = useNotifications();
  const [email, setEmail] = useState("");
  const [permissions, setPermissions] = useState<ServerPermission[]>(["view"]);

  useEffect(() => {
    if (!open) return;
    setEmail("");
    setPermissions(["view"]);
  }, [open]);

  const add = useMutation({
    mutationFn: () =>
      api.servers.addMember(serverId, {
        email: email.trim(),
        permissions: collapsePerms(permissions),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server-members", serverId] });
      success("Member added");
      onClose();
    },
    onError: (e: Error) => error("Add failed", e.message),
  });

  return (
    <Dialog open={open} onClose={onClose} title="Add Member" className="max-w-2xl">
      <div className="space-y-4">
        <div className="space-y-1.5">
          <Label>Email</Label>
          <Input
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="helper@example.com"
          />
        </div>
        <PermissionGrid value={permissions} onChange={setPermissions} />
        <div className="flex justify-end gap-3 pt-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            loading={add.isPending}
            disabled={!email.trim() || permissions.length === 0}
            onClick={() => add.mutate()}
          >
            Add
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

export function AccessTab({ serverId }: { serverId: string }) {
  const [showAdd, setShowAdd] = useState(false);
  const [removeTarget, setRemoveTarget] = useState<ServerMember | null>(null);
  const qc = useQueryClient();
  const { success, error } = useNotifications();

  const { data, isLoading } = useQuery({
    queryKey: ["server-members", serverId],
    queryFn: () => api.servers.members(serverId),
  });

  const remove = useMutation({
    mutationFn: (member: ServerMember) =>
      api.servers.removeMember(serverId, member.user_id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["server-members", serverId] });
      qc.invalidateQueries({ queryKey: ["server-permissions", serverId] });
      success("Member removed");
      setRemoveTarget(null);
    },
    onError: (e: Error) => error("Remove failed", e.message),
  });

  const members = data?.members ?? [];
  const owner = data?.owner;
  const ownerPermissions = useMemo(() => owner?.permissions ?? [], [owner]);

  return (
    <div className="space-y-5">
      <section className="rounded-md border border-border bg-surface">
        <div className="flex items-center justify-between gap-4 border-b border-border px-4 py-3 sm:px-5">
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-text-primary">Access</h2>
            <p className="mt-1 text-xs text-text-secondary">
              Direct per-server access for trusted helpers.
            </p>
          </div>
          <Button size="sm" onClick={() => setShowAdd(true)}>
            <Plus className="h-4 w-4" /> Add Member
          </Button>
        </div>

        {isLoading ? (
          <div className="flex justify-center py-12">
            <div className="h-6 w-6 animate-spin rounded-full border-2 border-accent border-t-transparent" />
          </div>
        ) : (
          <>
            {owner && (
              <div className="space-y-4 border-b border-border/60 px-4 py-4 sm:px-5">
                <MemberIdentity member={owner} />
                <PermissionGrid value={ownerPermissions} disabled />
              </div>
            )}
            {members.length === 0 ? (
              <EmptyState
                icon={ShieldCheck}
                title="No members yet"
                hint="The owner always keeps full access. Add trusted helpers when you want to delegate specific server work."
              />
            ) : (
              members.map((member) => (
                <MemberRow
                  key={member.user_id}
                  member={member}
                  onRemove={setRemoveTarget}
                />
              ))
            )}
          </>
        )}
      </section>

      <AddMemberDialog
        serverId={serverId}
        open={showAdd}
        onClose={() => setShowAdd(false)}
      />

      <ConfirmDialog
        open={removeTarget !== null}
        onClose={() => setRemoveTarget(null)}
        onConfirm={() => removeTarget && remove.mutate(removeTarget)}
        title="Remove member"
        description={`Remove access for ${removeTarget?.email ?? "this member"}?`}
        confirmLabel="Remove"
        variant="destructive"
        loading={remove.isPending}
      />
    </div>
  );
}
