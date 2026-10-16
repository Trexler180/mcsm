import { useEffect, useState } from 'react'
import { Btn } from './controls'
import { failureOf, type FailureWhy } from './failure'
import { setUi } from './store'
import { cx, plural } from './util'

/**
 * Unsaved changes. The same bar on every form. A save that doesn't land says
 * so in the bar itself and keeps the edits, so nothing typed is lost; if the
 * session ended, signing in again finishes the save.
 */
export function SaveBar({
  dirty,
  count,
  onSave,
  onReset,
  label,
  saving,
}: {
  dirty: boolean
  count?: number
  onSave: () => Promise<unknown> | void
  onReset: () => void
  label?: string
  saving?: boolean
}) {
  const [err, setErr] = useState<FailureWhy | null>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (!dirty) setErr(null)
  }, [dirty])
  if (!dirty) return null
  const save = async () => {
    setErr(null)
    setBusy(true)
    try {
      await onSave()
    } catch (e) {
      const f = failureOf(e)
      if (f.why === 'session') setUi({ reauth: { label: 'save your changes', retry: () => void save() } })
      else setErr(f.why)
    } finally {
      setBusy(false)
    }
  }
  const text = err
    ? err === 'down'
      ? 'Couldn’t save. The panel can’t be reached. Your changes are still here.'
      : 'Couldn’t save. Your changes are still here.'
    : label || (count ? plural(count, 'unsaved change') : 'Unsaved changes')
  return (
    <div className={cx('savebar', err && 'failed')} role="region" aria-label="Unsaved changes" aria-live="polite">
      <span>{text}</span>
      <Btn variant="quiet" size="sm" onClick={onReset} disabled={busy || saving}>
        Discard
      </Btn>
      <Btn variant="primary" size="sm" onClick={() => void save()} loading={busy || saving}>
        {err ? 'Try again' : 'Save'}
      </Btn>
    </div>
  )
}
