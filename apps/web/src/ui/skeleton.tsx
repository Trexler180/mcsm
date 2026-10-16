import type { CSSProperties, ReactNode } from 'react'
import type { UseQueryResult } from '@tanstack/react-query'
import { Btn } from './controls'
import { failureOf, type Failure } from './failure'
import { setUi } from './store'
import { cx } from './util'

// Loading and failing. Until data arrives each view shows a skeleton the
// shape of what it's about to show; if it doesn't arrive, "Couldn't load"
// takes its place and the rest of the panel keeps working.

/** Renders a query's data, its skeleton while it loads, or what went wrong.
 *  Data already on screen stays while a refetch runs or fails. */
export function Loaded<T>({
  query,
  sk,
  what = 'this page',
  page,
  children,
}: {
  query: UseQueryResult<T>
  sk: ReactNode
  what?: string
  /** Puts the error inside page padding, for views not already in a .page. */
  page?: boolean
  children: (data: T) => ReactNode
}) {
  if (query.data !== undefined) return <>{children(query.data)}</>
  if (query.isError) {
    const e = <LoadError what={what} failure={failureOf(query.error)} onRetry={() => void query.refetch()} />
    return page ? <div className="page">{e}</div> : e
  }
  return (
    <div className="sk-fade" style={{ display: 'contents' }} aria-busy="true" aria-label="Loading">
      {sk}
    </div>
  )
}

/** In place of whatever didn't load. */
export function LoadError({ what = 'this page', failure, onRetry }: { what?: string; failure: Failure; onRetry: () => void }) {
  const why = failure.why
  return (
    <div className="load-error" role="alert">
      <div className="empty-title">Couldn’t load {what}.</div>
      <p className="t2">
        {why === 'down'
          ? 'The panel can’t be reached right now. This tries again by itself when it’s back.'
          : why === 'session'
            ? 'Your session ended. Sign in again and it loads.'
            : why === 'timeout'
              ? 'The panel didn’t answer in time. That’s usually brief.'
              : failure.message}
      </p>
      <div className="row" style={{ gap: 12, marginTop: 14 }}>
        {why === 'session' ? (
          <Btn variant="primary" onClick={() => setUi({ reauth: { label: null, retry: onRetry } })}>
            Sign in again
          </Btn>
        ) : (
          <Btn onClick={onRetry}>Try again</Btn>
        )}
        <span className="t3 small">{why === 'down' ? 'No response' : why === 'session' ? 'Signed out' : why === 'timeout' ? 'Timed out' : null}</span>
      </div>
    </div>
  )
}

// ---- pieces ---------------------------------------------------------------------

const W = [72, 54, 88, 63, 47, 80, 69, 58, 91, 45, 76, 61]
const w = (i: number, min = 0) => Math.max(min, W[i % W.length])
const pct = (n: number) => n + '%'

export function Sk({ w: width = '100%', h = 13, r = 8, style }: { w?: number | string; h?: number; r?: number; style?: CSSProperties }) {
  return <span className="sk" style={{ width, height: h, borderRadius: r, ...style }} />
}

export function SkText({ lines = 2, gap = 8, last = 55 }: { lines?: number; gap?: number; last?: number }) {
  return (
    <div className="stack" style={{ gap }}>
      {Array.from({ length: lines }, (_, i) => (
        <Sk key={i} w={i === lines - 1 ? last + '%' : '100%'} />
      ))}
    </div>
  )
}

export function SkHead({ lede = true, action = true }: { lede?: boolean; action?: boolean }) {
  return (
    <div className="page-head">
      <div className="grow stack" style={{ gap: 10 }}>
        <Sk w={200} h={28} r={10} />
        {lede && <Sk w={340} h={14} />}
      </div>
      {action && <Sk w={130} h={34} r={999} />}
    </div>
  )
}

export function SkTable({ rows = 8, cols = 4, header = true, icons }: { rows?: number; cols?: number; header?: boolean; icons?: boolean }) {
  return (
    <div className="tile flush">
      {header && (
        <div className="sk-row" style={{ paddingTop: 12, paddingBottom: 10 }}>
          {Array.from({ length: cols }, (_, c) => (
            <Sk key={c} w={c === 0 ? '22%' : '12%'} h={10} style={{ flex: c === 0 ? '0 0 auto' : 1, opacity: 0.6 }} />
          ))}
        </div>
      )}
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="sk-row">
          {icons && <Sk w={34} h={34} r={8} />}
          <div className="stack" style={{ gap: 7, flex: '0 0 30%' }}>
            <Sk w={pct(w(i, 40))} h={13} />
            <Sk w={pct(w(i + 5, 30))} h={10} style={{ opacity: 0.6 }} />
          </div>
          {Array.from({ length: cols - 1 }, (_, c) => (
            <Sk key={c} w={pct(w(i + c + 2, 35))} h={12} style={{ flex: 1, maxWidth: 160 }} />
          ))}
          <Sk w={28} h={28} r={999} style={{ marginLeft: 'auto' }} />
        </div>
      ))}
    </div>
  )
}

export function SkItems({ rows = 6, icons }: { rows?: number; icons?: boolean }) {
  return (
    <div className="tile flush">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="sk-row" style={{ alignItems: 'flex-start' }}>
          {icons && <Sk w={44} h={44} r={10} />}
          <div className="stack grow" style={{ gap: 8 }}>
            <Sk w={pct(w(i, 25) / 2.2)} h={14} />
            <Sk w={pct(w(i + 3, 50))} h={11} style={{ opacity: 0.7 }} />
            <Sk w={pct(w(i + 6, 20) / 2)} h={10} style={{ opacity: 0.5 }} />
          </div>
          <Sk w={86} h={28} r={999} />
        </div>
      ))}
    </div>
  )
}

export function SkForm({ sections = 2 }: { sections?: number }) {
  return (
    <div>
      {Array.from({ length: sections }, (_, i) => (
        <div key={i} className="form-section">
          <Sk w={160} h={16} r={6} style={{ marginBottom: 10 }} />
          <Sk w="60%" h={12} style={{ marginBottom: 20, opacity: 0.7 }} />
          <div className="form-grid">
            {Array.from({ length: 4 }, (_, j) => (
              <div key={j} className="stack" style={{ gap: 8 }}>
                <Sk w={90} h={11} />
                <Sk h={38} r={12} />
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  )
}

// ---- whole views ---------------------------------------------------------------------

export function HomeSkeleton() {
  return (
    <div className="page home">
      <div className="home-head">
        <Sk w={320} h={20} r={6} />
        <Sk w={140} h={12} />
      </div>
      <section className="kpis">
        {Array.from({ length: 6 }, (_, i) => (
          <div key={i} className="kpi" style={{ gap: 8 }}>
            <Sk w={70} h={10} />
            <Sk w={44} h={18} r={6} />
            <Sk w="80%" h={10} style={{ opacity: 0.6 }} />
          </div>
        ))}
      </section>
      <section className="section">
        <Sk w={130} h={14} r={6} style={{ marginBottom: 12 }} />
        <div className="issues">
          {Array.from({ length: 3 }, (_, i) => (
            <div key={i} className="issue">
              <Sk w={24} h={24} r={6} />
              <div className="grow">
                <Sk w={pct(w(i, 50))} h={12} />
              </div>
              <Sk w={90} h={26} r={6} />
            </div>
          ))}
        </div>
      </section>
      <section className="section">
        <Sk w={90} h={14} r={6} style={{ marginBottom: 12 }} />
        <div className="srv-list">
          {Array.from({ length: 7 }, (_, i) => (
            <div key={i} className="srv-row" style={{ cursor: 'default' }}>
              <span className="cell">
                <Sk w={20} h={20} r={4} />
                <Sk w={pct(w(i, 45))} h={12} />
              </span>
              <span className="cell">
                <Sk w={80} h={12} />
              </span>
              <span className="cell r">
                <Sk w={36} h={12} />
              </span>
              <span className="cell r">
                <Sk w={28} h={12} />
              </span>
              <span className="cell r">
                <Sk w={28} h={12} />
              </span>
              <span className="cell">
                <Sk w={90} h={12} />
              </span>
              <span className="cell bars-cell">
                <span className="bars">
                  {Array.from({ length: 24 }, (_, j) => (
                    <i key={j} className="sk" style={{ height: 20 + ((j * 37) % 70) + '%', borderRadius: 2 }} />
                  ))}
                </span>
              </span>
              <span className="cell">
                <Sk w={50} h={12} />
              </span>
              <span className="cell">
                <Sk w={56} h={12} />
              </span>
            </div>
          ))}
        </div>
      </section>
      <div className="section widgets">
        {Array.from({ length: 3 }, (_, i) => (
          <div key={i} className="tile widget stack" style={{ gap: 12 }}>
            <Sk w={90} h={13} />
            {Array.from({ length: 4 }, (_, j) => (
              <Sk key={j} w={pct(w(i + j, 70))} h={11} style={{ opacity: 0.7 }} />
            ))}
          </div>
        ))}
      </div>
    </div>
  )
}

export function ListPageSkeleton({ narrow, items }: { narrow?: boolean; items?: boolean }) {
  return (
    <div className={cx('page', narrow && 'narrow')}>
      <SkHead />
      <div className="toolbar">
        <Sk w={240} h={38} r={999} />
        <Sk w={280} h={34} r={999} />
      </div>
      {items ? <SkItems rows={7} /> : <SkTable rows={9} />}
    </div>
  )
}

export function FormPageSkeleton({ subnav }: { subnav?: boolean }) {
  return (
    <div className={cx('page', !subnav && 'narrow')}>
      <SkHead lede={false} action={false} />
      {subnav ? (
        <div className="with-subnav">
          <div className="stack" style={{ gap: 8 }}>
            {Array.from({ length: 4 }, (_, i) => (
              <Sk key={i} h={30} r={10} w={pct(w(i, 60))} />
            ))}
          </div>
          <SkForm />
        </div>
      ) : (
        <SkForm sections={3} />
      )}
    </div>
  )
}

export function ConsoleSkeleton() {
  return (
    <div className="console-page">
      <div className="console-main">
        <div className="console">
          <div className="console-bar">
            <Sk w={220} h={34} r={999} />
            <span className="grow" />
            <Sk w={180} h={32} r={999} />
          </div>
          <div className="console-out" style={{ padding: '16px', display: 'flex', flexDirection: 'column', gap: 11, justifyContent: 'flex-end' }}>
            {Array.from({ length: 18 }, (_, i) => (
              <div key={i} className="row" style={{ gap: 10 }}>
                <Sk w={60} h={10} style={{ opacity: 0.5 }} />
                <Sk w={pct(w(i * 3, 20))} h={10} />
              </div>
            ))}
          </div>
          <Sk h={48} r={999} style={{ marginTop: 6 }} />
        </div>
      </div>
      <aside className="console-aside">
        {[6, 4, 3].map((n, i) => (
          <div key={i} className="aside-block stack" style={{ gap: 10 }}>
            <Sk w={100} h={13} />
            {i < 2 && <Sk w={90} h={30} r={10} />}
            {Array.from({ length: n }, (_, j) => (
              <div key={j} className="row" style={{ justifyContent: 'space-between' }}>
                <Sk w={w(j + i, 30) * 1.2} h={11} />
                <Sk w={36} h={11} style={{ opacity: 0.6 }} />
              </div>
            ))}
          </div>
        ))}
      </aside>
    </div>
  )
}

export function StatsSkeleton() {
  return (
    <>
      <div className="figures">
        {Array.from({ length: 4 }, (_, i) => (
          <div key={i} className="figure stack" style={{ gap: 10 }}>
            <Sk w={70} h={28} r={10} />
            <Sk w={110} h={11} />
          </div>
        ))}
      </div>
      <div className="chart-block">
        <Sk w={120} h={14} style={{ marginBottom: 18 }} />
        <Sk h={200} r={14} style={{ opacity: 0.7 }} />
      </div>
      <div className="chart-block split" style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(360px, 1fr))' }}>
        {[0, 1].map((i) => (
          <div key={i}>
            <Sk w={140} h={14} style={{ marginBottom: 18 }} />
            <Sk h={170} r={14} style={{ opacity: 0.7 }} />
          </div>
        ))}
      </div>
    </>
  )
}

export function TabSkeleton({ tab }: { tab: string }) {
  if (tab === 'stats')
    return (
      <>
        <div className="toolbar" style={{ marginBottom: 24 }}>
          <Sk w={280} h={34} r={999} />
        </div>
        <StatsSkeleton />
      </>
    )
  if (tab === 'settings')
    return (
      <div className="with-subnav">
        <div className="stack" style={{ gap: 8 }}>
          {Array.from({ length: 4 }, (_, i) => (
            <Sk key={i} h={30} r={10} w={pct(w(i, 60))} />
          ))}
        </div>
        <SkForm />
      </div>
    )
  if (tab === 'schedules')
    return (
      <>
        <div className="toolbar" style={{ marginBottom: 20 }}>
          <Sk w={360} h={14} />
          <span className="grow" />
          <Sk w={130} h={34} r={999} />
        </div>
        <SkItems rows={5} />
      </>
    )
  return (
    <>
      <div className="toolbar" style={{ marginBottom: 20 }}>
        <Sk w={tab === 'players' ? 420 : 300} h={34} r={999} />
        <span className="grow" />
        <Sk w={200} h={34} r={999} />
        <Sk w={120} h={34} r={999} />
      </div>
      <SkTable rows={tab === 'worlds' ? 2 : 10} cols={4} icons={tab === 'mods'} />
    </>
  )
}

export function FilesSkeleton() {
  return (
    <div style={{ padding: '14px var(--pad)', flex: 1, minHeight: 0, overflow: 'hidden' }}>
      <div className="toolbar">
        <Sk w={220} h={14} />
        <span className="grow" />
        <Sk w={28} h={28} r={999} />
        <Sk w={86} h={28} r={999} />
      </div>
      <SkTable rows={12} />
    </div>
  )
}

export function SheetSkeleton() {
  return (
    <div className="stack" style={{ gap: 18 }}>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: '12px 24px' }}>
        {Array.from({ length: 6 }, (_, i) => (
          <div key={i} className="row" style={{ gap: 14, minWidth: 0, overflow: 'hidden' }}>
            <Sk w={70} h={11} style={{ opacity: 0.6 }} />
            <Sk w={pct(w(i, 40))} h={12} />
          </div>
        ))}
      </div>
      <Sk w={330} h={40} r={999} />
      {Array.from({ length: 7 }, (_, i) => (
        <div key={i} className="row" style={{ gap: 14 }}>
          <div className="stack grow" style={{ gap: 7 }}>
            <Sk w={pct(w(i, 30) / 2)} h={13} />
            <Sk w={pct(w(i + 4, 30) / 1.5)} h={10} style={{ opacity: 0.6 }} />
          </div>
          <Sk w={70} h={11} />
        </div>
      ))}
    </div>
  )
}
