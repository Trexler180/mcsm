import { clsx } from 'clsx'

/**
 * A content-shaped placeholder shown while data loads. Preferred over a bare
 * spinner: it holds the layout so the page doesn't jump when data arrives, and
 * reads as "almost ready" rather than "nothing here yet".
 */
export function Skeleton({
  className,
  ...props
}: React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={clsx('animate-pulse rounded-md bg-surface-2', className)}
      {...props}
    />
  )
}

/**
 * A stack of placeholder rows shaped like the bordered list items used across
 * the server tabs (backups, worlds, tasks, access…). Drop-in replacement for a
 * centered spinner so the tab keeps its shape while the list loads.
 */
export function SkeletonList({
  rows = 4,
  className,
}: {
  rows?: number
  className?: string
}) {
  return (
    <div className={clsx('space-y-2', className)}>
      {Array.from({ length: rows }).map((_, i) => (
        <div
          key={i}
          className="flex items-center gap-3 rounded-lg border border-border bg-surface p-3"
        >
          <Skeleton className="h-9 w-9 flex-shrink-0 rounded-md" />
          <div className="min-w-0 flex-1 space-y-2">
            <Skeleton className="h-3.5 w-1/3" />
            <Skeleton className="h-3 w-1/2" />
          </div>
          <Skeleton className="h-7 w-16 flex-shrink-0" />
        </div>
      ))}
    </div>
  )
}
