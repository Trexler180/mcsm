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
    <div className="fixed inset-0 z-50 flex items-center justify-center p-4">
      <div
        ref={overlayRef}
        className="absolute inset-0 bg-black/60"
        onClick={onClose}
      />
      <div
        className={clsx(
          'relative z-10 max-h-[90vh] w-[calc(100vw-2rem)] max-w-md overflow-y-auto rounded-lg border border-border bg-surface p-6 shadow-xl',
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
}: ConfirmDialogProps) {
  return (
    <Dialog open={open} onClose={onClose} title={title} description={description}>
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
