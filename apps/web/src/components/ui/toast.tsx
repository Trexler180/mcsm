import { clsx } from 'clsx'
import { X, CheckCircle, AlertCircle, AlertTriangle } from 'lucide-react'
import { useNotifications, type Toast, type ToastVariant } from '@/store/notifications'

const variantConfig: Record<ToastVariant, { icon: React.ReactNode; classes: string }> = {
  default: {
    icon: null,
    classes: 'bg-surface border-border',
  },
  success: {
    icon: <CheckCircle className="h-4 w-4 text-green-400 flex-shrink-0" />,
    classes: 'bg-surface border-green-800/50',
  },
  error: {
    icon: <AlertCircle className="h-4 w-4 text-red-400 flex-shrink-0" />,
    classes: 'bg-surface border-red-800/50',
  },
  warning: {
    icon: <AlertTriangle className="h-4 w-4 text-yellow-400 flex-shrink-0" />,
    classes: 'bg-surface border-yellow-800/50',
  },
}

function ToastItem({ toast }: { toast: Toast }) {
  const { remove } = useNotifications()
  const config = variantConfig[toast.variant]

  return (
    <div
      className={clsx(
        'flex items-start gap-3 rounded-lg border p-3 shadow-lg',
        'min-w-[260px] max-w-[340px]',
        config.classes,
      )}
    >
      {config.icon}
      <div className="flex-1 min-w-0">
        <p className="text-sm font-medium text-text-primary">
          {toast.title}
          {toast.count > 1 && (
            <span className="ml-1.5 rounded-full bg-border px-1.5 py-0.5 text-[10px] font-semibold text-text-secondary align-middle">
              ×{toast.count}
            </span>
          )}
        </p>
        {toast.description && (
          <p className="text-xs text-text-secondary mt-0.5">{toast.description}</p>
        )}
      </div>
      <button
        onClick={() => remove(toast.id)}
        className="text-text-secondary hover:text-text-primary flex-shrink-0"
      >
        <X className="h-4 w-4" />
      </button>
    </div>
  )
}

const MAX_VISIBLE = 3

export function Toaster() {
  const { toasts } = useNotifications()
  if (toasts.length === 0) return null

  // Newest at the bottom (closest to corner); only render the last few so the
  // stack can't grow tall enough to cover the UI behind it.
  const visible = toasts.slice(-MAX_VISIBLE)
  const hidden = toasts.length - visible.length

  return (
    <div className="fixed bottom-[max(1rem,env(safe-area-inset-bottom))] left-4 right-4 z-[100] flex flex-col gap-2 items-end sm:left-auto">
      {hidden > 0 && (
        <span className="rounded-full bg-surface border border-border px-2 py-0.5 text-[10px] text-text-secondary shadow">
          +{hidden} more
        </span>
      )}
      {visible.map((t) => (
        <ToastItem key={t.id} toast={t} />
      ))}
    </div>
  )
}
