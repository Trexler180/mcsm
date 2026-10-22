import { useNotifications, type ToastVariant } from '@/store/notifications'
import { Icon } from './icon'
import { cx } from './util'

// Toasts. The queue is the app's notification store, so everything that
// already raises a toast shows here in the new style.

export type ToastTone = 'ok' | 'bad' | 'warn' | 'info'

export interface ToastActionSpec {
  label: string
  run: () => void | Promise<void>
}

const VARIANT: Record<ToastTone, ToastVariant> = { ok: 'success', bad: 'error', warn: 'warning', info: 'default' }

/** Raises a toast. Up to two actions ("Try again", "Undo"); a toast with an
 *  action that asks something of the user can be made sticky. */
export function toast(
  tone: ToastTone,
  title: string,
  body?: string,
  opts: { actions?: ToastActionSpec[]; sticky?: boolean; key?: string } = {},
) {
  const [first, second] = opts.actions ?? []
  useNotifications.getState().add({
    title,
    description: body || undefined,
    variant: VARIANT[tone],
    sticky: opts.sticky,
    key: opts.key,
    action: first ? { label: first.label, onClick: first.run } : undefined,
    secondary: second ? { label: second.label, onClick: second.run } : undefined,
  })
}

export function Toasts() {
  const toasts = useNotifications((s) => s.toasts)
  const remove = useNotifications((s) => s.remove)
  const runAction = useNotifications((s) => s.runAction)
  const hold = useNotifications((s) => s.hold)
  const release = useNotifications((s) => s.release)
  return (
    <div className="toasts" aria-live="polite">
      {toasts.map((t) => (
        <div key={t.id} className={cx('toast', t.variant === 'error' && 'bad')} role="status" onMouseEnter={() => hold(t.id)} onMouseLeave={() => release(t.id)}>
          <span className="toast-text">
            {t.title}
            {t.count > 1 && <span className="toast-sub num">×{t.count}</span>}
            {t.description && <span className="toast-sub">{t.description}</span>}
          </span>
          {t.action && (
            <button type="button" disabled={t.busy} onClick={() => void runAction(t.id, 'action')}>
              {t.action.label}
            </button>
          )}
          {t.secondary && (
            <button type="button" disabled={t.busy} onClick={() => void runAction(t.id, 'secondary')}>
              {t.secondary.label}
            </button>
          )}
          <button type="button" className="x" aria-label="Dismiss" onClick={() => remove(t.id)}>
            <Icon name="x" size={14} />
          </button>
        </div>
      ))}
    </div>
  )
}
