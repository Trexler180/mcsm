import { useEffect, useRef, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { McpApprovalDialog } from '@/components/layout/mcp-approval-dialog'
import { RebootOverlay } from '@/components/layout/reboot-overlay'
import { seedNotificationFeed } from '@/lib/notify-stream'
import { useConnection } from '@/live/connection'
import { startLive, stopLive } from '@/live/events'
import { liveHandlers } from '@/live/wire'
import { IconBtn } from '@/ui/controls'
import { BrandMark } from '@/ui/icon'
import { ConfirmHost, TipHost } from '@/ui/overlays'
import { StepUpHost } from '@/ui/stepup'
import { setUi, useUi } from '@/ui/store'
import { Toasts } from '@/ui/toasts'
import { cx } from '@/ui/util'
import { ConnBanner } from './banners'
import { useFleet } from './fleet'
import { useSection } from './nav'
import { Palette } from './palette'
import { ReauthHost, useSessionEndedPrompt } from './reauth'
import { Sidebar } from './sidebar'

/** Sections already on the new design. Everything else is an older screen,
 *  shown inside the shell until its turn comes. */
const PORTED = new Set<string>(['home', 'server', 'alerts', 'nodes', 'people'])

/** The signed-in panel: sidebar, main area, and everything that can appear
 *  over any page. */
export function AppShell({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  const navOpen = useUi((s) => s.navOpen)
  const conn = useConnection((s) => s.state)
  const { pathname, section } = useSection()
  const fleet = useFleet()
  useSessionEndedPrompt(true)

  // Alerts: the feed once, then live over the event stream.
  useEffect(() => {
    void seedNotificationFeed()
  }, [])

  // The live stream starts from the sequence the fleet was fetched at, so
  // nothing between the fetch and the connection is missed. If the fleet
  // can't load, it starts from now and catches up when the panel is back.
  const started = useRef(false)
  useEffect(() => {
    if (started.current) return
    if (fleet.data) {
      started.current = true
      startLive(liveHandlers(qc), fleet.data.event_seq)
    } else if (fleet.isError) {
      started.current = true
      startLive(liveHandlers(qc), 0)
    }
  }, [fleet.data, fleet.isError, qc])
  useEffect(() => () => stopLive(), [])

  useEffect(() => {
    setUi({ navOpen: false })
    document.querySelectorAll('.scroll, .legacy-page').forEach((el) => {
      el.scrollTop = 0
    })
  }, [pathname])

  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setUi({ palette: !useUi.getState().palette })
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [])

  // Cursor-follow glow on tiles marked .glow.
  useEffect(() => {
    const h = (e: PointerEvent) => {
      const el = e.target instanceof Element ? e.target.closest<HTMLElement>('.glow') : null
      if (!el) return
      const r = el.getBoundingClientRect()
      el.style.setProperty('--mx', e.clientX - r.left + 'px')
      el.style.setProperty('--my', e.clientY - r.top + 'px')
    }
    document.addEventListener('pointermove', h, { passive: true })
    return () => document.removeEventListener('pointermove', h)
  }, [])

  return (
    <div className={cx('app', navOpen && 'nav-open', conn !== 'up' && 'stale')}>
      <Sidebar onNavigate={() => setUi({ navOpen: false })} />
      <main className="main">
        <div className="mobilebar">
          <IconBtn icon="menu" label="Menu" size="" onClick={() => setUi({ navOpen: true })} />
          <span className="strong grow row" style={{ gap: 8 }}>
            <BrandMark size={20} />
            MCSM
          </span>
          <IconBtn icon="search" label="Search" size="" onClick={() => setUi({ palette: true })} />
        </div>
        <ConnBanner />
        {/* Static import on purpose: if the panel's own host is the one
            restarting, a lazily loaded chunk could not load. */}
        <RebootOverlay />
        {PORTED.has(section) ? children : <div className="legacy-page">{children}</div>}
      </main>
      {navOpen && <div className="sheet-scrim" style={{ zIndex: 74 }} onClick={() => setUi({ navOpen: false })} />}
      <Palette />
      {/* An agent's request has to be answerable from whatever screen the
          operator is on. */}
      <McpApprovalDialog />
      <ConfirmHost />
      <StepUpHost />
      <ReauthHost />
      <Toasts />
      <TipHost />
    </div>
  )
}

/** Shown while the session is being checked on first load. */
export function BootScreen() {
  return (
    <div style={{ height: '100%', display: 'grid', placeItems: 'center' }} aria-busy="true" aria-label="Loading">
      <div className="stack" style={{ alignItems: 'center', gap: 12, color: 'var(--text-3)' }}>
        <BrandMark size={28} />
      </div>
    </div>
  )
}
