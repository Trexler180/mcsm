import {
  ChevronRight,
  FileCog,
  FileText,
  FolderTree,
  Globe2,
  HardDrive,
  LayoutDashboard,
  PackageOpen,
  Rocket,
  ShieldCheck,
  SlidersHorizontal,
  Terminal,
  ToggleRight,
  Users,
} from "lucide-react";
import type { ServerPermission } from "@/lib/types";

export type ServerSection =
  | "dashboard"
  | "console"
  | "logs"
  | "players"
  | "mods"
  | "version"
  | "options"
  | "properties"
  | "configs"
  | "files"
  | "worlds"
  | "backups"
  | "tasks"
  | "access";

// Canonical list of the sections within a server, shared by the server-detail
// sidebar/picker and the command palette so they never drift apart.
export const SERVER_SECTIONS: Array<{
  value: ServerSection;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  group: string;
  permission: ServerPermission;
}> = [
  { value: "dashboard", label: "Dashboard", icon: LayoutDashboard, group: "Operate", permission: "view" },
  { value: "console", label: "Console", icon: Terminal, group: "Operate", permission: "console" },
  { value: "logs", label: "Logs", icon: FileText, group: "Operate", permission: "files" },
  { value: "players", label: "Players", icon: Users, group: "Operate", permission: "players" },
  { value: "mods", label: "Mods", icon: PackageOpen, group: "Manage", permission: "mods" },
  { value: "version", label: "Version", icon: Rocket, group: "Manage", permission: "settings" },
  { value: "options", label: "Options", icon: SlidersHorizontal, group: "Manage", permission: "settings" },
  { value: "properties", label: "Properties", icon: FileCog, group: "Manage", permission: "settings" },
  { value: "configs", label: "Configs", icon: FileCog, group: "Manage", permission: "files" },
  { value: "access", label: "Access", icon: ShieldCheck, group: "Manage", permission: "admin" },
  { value: "files", label: "Files", icon: FolderTree, group: "Storage", permission: "files" },
  { value: "worlds", label: "Worlds", icon: Globe2, group: "Storage", permission: "files" },
  { value: "backups", label: "Backups", icon: HardDrive, group: "Storage", permission: "backups" },
  { value: "tasks", label: "Tasks", icon: ToggleRight, group: "Automation", permission: "tasks" },
];

export function Panel({
  title,
  description,
  actions,
  children,
  onClick,
}: {
  title: string;
  description?: string;
  actions?: React.ReactNode;
  children: React.ReactNode;
  onClick?: () => void;
}) {
  const clickable = !!onClick;
  return (
    <section
      className={`rounded-md border border-border bg-surface ${
        clickable ? "cursor-pointer transition-colors hover:border-border-hover" : ""
      }`}
      onClick={onClick}
      role={clickable ? "button" : undefined}
      tabIndex={clickable ? 0 : undefined}
      onKeyDown={
        clickable
          ? (e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onClick();
              }
            }
          : undefined
      }
    >
      <div className="flex items-center justify-between gap-4 border-b border-border px-4 py-3 sm:px-5 sm:py-4">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-text-primary">{title}</h2>
          {description && (
            <p className="mt-1 text-xs text-text-secondary">{description}</p>
          )}
        </div>
        {actions ??
          (clickable && (
            <ChevronRight className="h-4 w-4 flex-shrink-0 text-text-secondary" />
          ))}
      </div>
      <div className="p-4 sm:p-5">{children}</div>
    </section>
  );
}

export function StatTile({
  label,
  value,
  detail,
}: {
  label: string;
  value: string;
  detail?: string;
}) {
  return (
    <div className="rounded-md border border-border bg-surface px-4 py-3">
      <p className="text-xs text-text-secondary">{label}</p>
      <p className="mt-1 truncate text-lg font-semibold text-text-primary">
        {value}
      </p>
      {detail && <p className="mt-1 text-xs text-text-secondary">{detail}</p>}
    </div>
  );
}
