import { clsx } from 'clsx'
import type { ServerStatus } from '@/lib/types'

type Variant = 'default' | 'success' | 'warning' | 'error' | 'muted'

interface BadgeProps extends React.HTMLAttributes<HTMLSpanElement> {
  variant?: Variant
}

const variantClasses: Record<Variant, string> = {
  default: 'bg-surface-2 text-text-primary',
  success: 'bg-green-900/40 text-green-400 border border-green-800/50',
  warning: 'bg-yellow-900/40 text-yellow-400 border border-yellow-800/50',
  error: 'bg-red-900/40 text-red-400 border border-red-800/50',
  muted: 'bg-surface-2 text-text-secondary',
}

export function Badge({ variant = 'default', className, children, ...props }: BadgeProps) {
  return (
    <span
      className={clsx(
        'inline-flex items-center gap-1 rounded-full px-2.5 py-0.5 text-xs font-medium',
        variantClasses[variant],
        className,
      )}
      {...props}
    >
      {children}
    </span>
  )
}

const statusVariant: Record<ServerStatus, Variant> = {
  online: 'success',
  starting: 'warning',
  stopping: 'warning',
  offline: 'muted',
  crashed: 'error',
  startup_failure: 'warning',
  error: 'error',
}

const statusDot: Record<ServerStatus, string> = {
  online: 'bg-green-400',
  starting: 'bg-yellow-400 animate-pulse',
  stopping: 'bg-yellow-400 animate-pulse',
  offline: 'bg-gray-500',
  crashed: 'bg-red-400',
  startup_failure: 'bg-orange-400',
  error: 'bg-red-400',
}

export function StatusBadge({ status }: { status: ServerStatus }) {
  return (
    <Badge variant={statusVariant[status]}>
      <span className={clsx('w-1.5 h-1.5 rounded-full', statusDot[status])} />
      {status}
    </Badge>
  )
}
