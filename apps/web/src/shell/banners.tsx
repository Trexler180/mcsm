import { useEffect, useState } from 'react'
import { useConnection } from '@/live/connection'
import { Btn } from '@/ui/controls'
import { fmtClock } from '@/ui/format'
import { cx } from '@/ui/util'

export function useTicker(on: boolean, ms = 1000) {
  const [, set] = useState(0)
  useEffect(() => {
    if (!on) return
    const t = setInterval(() => set((n) => n + 1), ms)
    return () => clearInterval(t)
  }, [on, ms])
}

/** Live updates paused, or the panel unreachable. Says how old the screen is
 *  and when it tries again, and lets you try now. */
export function ConnBanner() {
  const c = useConnection()
  useTicker(c.state !== 'up', 500)
  if (c.state === 'up') return null
  const now = Date.now()
  const secs = c.nextAt ? Math.max(0, Math.ceil((c.nextAt - now) / 1000)) : 0
  const down = c.state === 'down'
  // Between a try starting and the next one being scheduled.
  const checking = c.nextAt == null || (c.checkedAt != null && now - c.checkedAt < 1500 && c.attempt > 0)
  const since = c.since != null ? fmtClock(c.since) : 'a moment ago'
  return (
    <div className={cx('progress-banner conn-banner', down && 'bad')} role="status">
      <span className="grow">
        {down ? (
          <>
            <b>Can’t reach the panel.</b> What you see is from {since} and won’t change until it’s back. {checking ? 'Still no answer.' : `Trying again in ${secs} s.`}
          </>
        ) : (
          <>
            <b>Live updates paused.</b> {checking ? 'Still reconnecting.' : `Reconnecting in ${secs} s.`} Numbers and the console are from {since}.
          </>
        )}
      </span>
      <Btn size="sm" onClick={c.tryNow}>
        Try now
      </Btn>
    </div>
  )
}
