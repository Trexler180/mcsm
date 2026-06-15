import { clsx } from 'clsx'
import type { LucideIcon } from 'lucide-react'

/**
 * Shared empty-state placeholder: an icon, a headline, an optional hint, and an
 * optional call-to-action. Replaces the bare one-line "No X yet" strings that
 * made empty tabs feel broken rather than simply unpopulated.
 */
export function EmptyState({
  icon: Icon,
  title,
  hint,
  action,
  className,
}: {
  icon?: LucideIcon
  title: string
  hint?: string
  action?: React.ReactNode
  className?: string
}) {
  return (
    <div
      className={clsx(
        'flex flex-col items-center justify-center gap-3 px-6 py-12 text-center',
        className,
      )}
    >
      {Icon ? (
        <div className="flex h-11 w-11 items-center justify-center rounded-full border border-border bg-background">
          <Icon className="h-5 w-5 text-text-secondary" />
        </div>
      ) : null}
      <div className="space-y-1">
        <p className="text-sm font-medium text-text-primary">{title}</p>
        {hint ? (
          <p className="mx-auto max-w-sm text-xs text-text-secondary">{hint}</p>
        ) : null}
      </div>
      {action ? <div className="mt-1">{action}</div> : null}
    </div>
  )
}
