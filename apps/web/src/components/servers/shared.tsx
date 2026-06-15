import { ChevronRight } from "lucide-react";

export type ServerSection =
  | "dashboard"
  | "console"
  | "logs"
  | "players"
  | "mods"
  | "options"
  | "properties"
  | "configs"
  | "files"
  | "worlds"
  | "backups"
  | "tasks";

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
