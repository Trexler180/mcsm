import { useEffect, useRef } from 'react'
import { clsx } from 'clsx'
import { X } from 'lucide-react'
import { Button } from './button'

interface DialogProps {
  open: boolean
  onClose: () => void
  title: string
  description?: string
  children: React.ReactNode
  className?: string
  titleIcon?: React.ReactNode
  headerClassName?: string
  titleClassName?: string
  closeClassName?: string
}

export function Dialog({
  open,
  onClose,
  title,
  description,
  children,
  className,
  titleIcon,
  headerClassName,
  titleClassName,
  closeClassName,
}: DialogProps) {
  const overlayRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    if (open) document.addEventListener('keydown', handleKey)
    return () => document.removeEventListener('keydown', handleKey)
  }, [open, onClose])

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-end justify-center sm:items-center sm:p-4">
      <div
        ref={overlayRef}
        className="absolute inset-0 bg-black/60"
        onClick={onClose}
      />
      {/* Phones get a bottom sheet (full width, rounded top, safe-area
          padding for the home bar); sm+ keeps the centered card. */}
      <div
        className={clsx(
          'relative z-10 max-h-[85dvh] w-full max-w-md overflow-y-auto rounded-t-xl border border-border bg-surface p-4 pb-[max(1rem,env(safe-area-inset-bottom))] shadow-xl',
          'sm:max-h-[90dvh] sm:w-[calc(100vw-2rem)] sm:rounded-lg sm:p-6 sm:pb-6',
          className,
        )}
      >
        <div className={clsx("flex items-center justify-between mb-4", headerClassName)}>
          <div className="flex min-w-0 items-center gap-3">
            {titleIcon}
            <div className="min-w-0">
              <h2
                className={clsx(
                  "truncate text-lg font-semibold text-text-primary",
                  titleClassName,
                )}
              >
                {title}
              </h2>
              {description && (
                <p className="text-sm text-text-secondary mt-0.5">{description}</p>
              )}
            </div>
          </div>
          <Button
            variant="ghost"
            size="icon"
            onClick={onClose}
            className={closeClassName}
          >
            <X className="h-4 w-4" />
          </Button>
        </div>
        {children}
      </div>
    </div>
  )
}

interface ConfirmDialogProps {
  open: boolean
  onClose: () => void
  onConfirm: () => void
  title: string
  description: string
  confirmLabel?: string
  variant?: 'default' | 'destructive'
  loading?: boolean
  children?: React.ReactNode
}

export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  description,
  confirmLabel = 'Confirm',
  variant = 'default',
  loading,
  children,
}: ConfirmDialogProps) {
  return (
    <Dialog open={open} onClose={onClose} title={title} description={description}>
      {children}
      <div className="flex gap-3 justify-end mt-6">
        <Button variant="outline" onClick={onClose} disabled={loading}>
          Cancel
        </Button>
        <Button variant={variant} onClick={onConfirm} loading={loading}>
          {confirmLabel}
        </Button>
      </div>
    </Dialog>
  )
}
