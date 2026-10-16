import {
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type CSSProperties,
  type ReactNode,
} from 'react'
import { createPortal } from 'react-dom'
import type { ServerPermission } from '@/lib/types'
import { Icon, type IconName } from './icon'
import { deniedTip, useCan } from './perm'
import { cap, clamp, copyText, cx, pad2 } from './util'

// ---- status -----------------------------------------------------------------------------

export type StatusKey =
  | 'online'
  | 'starting'
  | 'stopping'
  | 'crashed'
  | 'startup_failure'
  | 'error'
  | 'offline'
  | 'unknown'
  | 'unreachable'

type Tone = 'ok' | 'warn' | 'bad' | 'hollow' | ''

export const STATE: Record<StatusKey, [string, Tone]> = {
  online: ['Running', 'ok'],
  starting: ['Starting', 'warn'],
  stopping: ['Stopping', 'warn'],
  crashed: ['Crashed', 'bad'],
  startup_failure: ['Failed to start', 'bad'],
  error: ['Error', 'bad'],
  offline: ['Stopped', ''],
  // The node hasn't answered, so the server's real state isn't known.
  unknown: ['Node offline', 'hollow'],
  unreachable: ['Node offline', 'hollow'],
}

function stateOf(status: string): [string, Tone] {
  return STATE[status as StatusKey] ?? [cap(status), '']
}

export function Status({ status, label, quiet }: { status: string; label?: string; quiet?: boolean }) {
  const [l, tone] = stateOf(status)
  return (
    <span className="status">
      <span className={cx('dot', tone)} />
      {!quiet && <span className={tone === 'bad' ? 'bad' : undefined}>{label || l}</span>}
    </span>
  )
}

/** A pill showing a server's state. Running gets a live, pinging dot. */
export function StatePill({ status, label }: { status: string; label?: string }) {
  const [l, tone] = stateOf(status)
  return (
    <span className={cx('state', tone !== 'hollow' && tone)}>
      <span className={cx('dot', tone)} />
      {label || l}
    </span>
  )
}

// ---- numbers that ease ------------------------------------------------------------------

export function Num({ value, format = (v) => String(Math.round(v)), duration = 700 }: { value: number; format?: (v: number) => string; duration?: number }) {
  const [shown, setShown] = useState(value)
  const cur = useRef(value)
  const raf = useRef(0)
  useEffect(() => {
    const a = cur.current
    const b = value
    if (a === b || typeof b !== 'number' || Number.isNaN(a)) {
      cur.current = b
      setShown(b)
      return
    }
    const start = performance.now()
    cancelAnimationFrame(raf.current)
    const step = (t: number) => {
      const k = Math.min(1, (t - start) / duration)
      const v = a + (b - a) * (1 - Math.pow(1 - k, 3))
      cur.current = v
      setShown(v)
      if (k < 1) raf.current = requestAnimationFrame(step)
    }
    raf.current = requestAnimationFrame(step)
    return () => cancelAnimationFrame(raf.current)
  }, [value, duration])
  return <span className="num">{format(shown)}</span>
}

// ---- a highlight that slides to the selected child ------------------------------------

export function useSlider<T extends HTMLElement = HTMLDivElement>(selector: string) {
  const ref = useRef<T>(null)
  const [box, setBox] = useState<{ x: number; y: number; w: number; h: number } | null>(null)
  const [ready, setReady] = useState(false)
  const measure = () => {
    const root = ref.current
    if (!root) return
    const el = root.querySelector<HTMLElement>(selector)
    if (!el) {
      setBox((b) => (b ? null : b))
      return
    }
    const next = { x: el.offsetLeft, y: el.offsetTop, w: el.offsetWidth, h: el.offsetHeight }
    setBox((b) => (b && b.x === next.x && b.y === next.y && b.w === next.w && b.h === next.h ? b : next))
  }
  // Re-measured after every render: the selected child can move whenever
  // anything above it changes.
  useLayoutEffect(measure)
  useEffect(() => {
    const id = requestAnimationFrame(() => setReady(true))
    const ro = new ResizeObserver(() => measure())
    if (ref.current) ro.observe(ref.current)
    return () => {
      cancelAnimationFrame(id)
      ro.disconnect()
    }
    // measure reads the latest DOM; the observer only needs setting up once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  const el = (
    <span
      className="slider"
      aria-hidden="true"
      style={
        box
          ? { width: box.w, height: box.h, transform: `translate(${box.x}px, ${box.y}px)`, left: 0, top: 0, transition: ready ? undefined : 'none' }
          : { opacity: 0, left: 0, top: 0 }
      }
    />
  )
  return [ref, el] as const
}

// ---- buttons ------------------------------------------------------------------------------

export interface BtnProps extends Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> {
  /** primary, quiet, danger, or a combination ("danger primary"). */
  variant?: string
  size?: 'sm' | 'lg' | ''
  icon?: IconName
  iconRight?: IconName
  loading?: boolean
  /** A permission on the current server; without it the button is disabled
   *  and says why. */
  need?: ServerPermission
  tip?: string
  children?: ReactNode
}

export function Btn({ variant, size, icon, iconRight, loading, disabled, need, children, className, tip, type = 'button', ...rest }: BtnProps) {
  const allowed = useCan()
  const denied = !!need && !allowed(need)
  return (
    <button
      type={type}
      className={cx('btn', variant, size, children == null && 'icon-only', className)}
      disabled={disabled || denied || loading}
      data-tip={denied && need ? deniedTip(need) : tip}
      aria-busy={loading || undefined}
      {...rest}
    >
      {loading ? <Icon name="refresh" size={14} className="spin" /> : icon && <Icon name={icon} size={size === 'sm' ? 14 : 15} />}
      {children}
      {iconRight && <Icon name={iconRight} size={14} />}
    </button>
  )
}

export function IconBtn({ icon, label, size = 'sm', variant = 'quiet', ...rest }: Omit<BtnProps, 'children'> & { icon: IconName; label: string }) {
  return <Btn icon={icon} size={size} variant={variant} aria-label={label} tip={label} {...rest} />
}

export function TextBtn({
  children,
  onClick,
  quiet,
  need,
  disabled,
  className,
}: {
  children: ReactNode
  onClick?: () => void
  quiet?: boolean
  need?: ServerPermission
  disabled?: boolean
  className?: string
}) {
  const allowed = useCan()
  const denied = !!need && !allowed(need)
  return (
    <button
      type="button"
      className={cx('link', quiet && 'quiet', className)}
      onClick={onClick}
      disabled={disabled || denied}
      data-tip={denied && need ? deniedTip(need) : undefined}
      style={disabled || denied ? { opacity: 0.45, cursor: 'not-allowed' } : undefined}
    >
      {children}
    </button>
  )
}

// ---- inputs ---------------------------------------------------------------------------------

export function Switch({ checked, onChange, disabled, label, need }: { checked: boolean; onChange?: (v: boolean) => void; disabled?: boolean; label?: string; need?: ServerPermission }) {
  const allowed = useCan()
  const denied = !!need && !allowed(need)
  return (
    <button
      type="button"
      role="switch"
      aria-checked={!!checked}
      aria-label={label}
      className="switch"
      disabled={disabled || denied}
      data-tip={denied && need ? deniedTip(need) : undefined}
      onClick={(e) => {
        e.stopPropagation()
        onChange?.(!checked)
      }}
    />
  )
}

export function Check({
  checked,
  onChange,
  label,
  indeterminate,
  disabled,
  hideLabel,
}: {
  checked: boolean
  onChange?: (v: boolean) => void
  label?: ReactNode
  indeterminate?: boolean
  disabled?: boolean
  hideLabel?: boolean
}) {
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = !!indeterminate
  }, [indeterminate])
  return (
    <label className="check" onClick={(e) => e.stopPropagation()}>
      <input ref={ref} type="checkbox" checked={!!checked} disabled={disabled} onChange={(e) => onChange?.(e.target.checked)} />
      {label != null && <span className={hideLabel ? 'sr-only' : undefined}>{label}</span>}
    </label>
  )
}

export interface ChoiceOption<V extends string = string> {
  value: V
  label: ReactNode
  desc?: ReactNode
  disabled?: boolean
  tip?: string
}

export function Choices<V extends string>({ value, onChange, options, columns }: { value: V; onChange: (v: V) => void; options: ChoiceOption<V>[]; columns?: number }) {
  return (
    <div className="choices" role="radiogroup" style={columns ? { gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` } : undefined}>
      {options.map((o) => (
        <button key={o.value} type="button" role="radio" aria-checked={value === o.value} className="choice" disabled={o.disabled} onClick={() => onChange(o.value)} data-tip={o.tip}>
          <span className="ring" />
          <span className="grow">
            <span style={{ fontWeight: 500 }}>{o.label}</span>
            {o.desc && (
              <span className="field-hint" style={{ display: 'block' }}>
                {o.desc}
              </span>
            )}
          </span>
        </button>
      ))}
    </div>
  )
}

export interface FilterOption<V extends string = string> {
  value: V
  label: ReactNode
  count?: number
}

export function Filters<V extends string>({ value, onChange, options, label }: { value: V; onChange: (v: V) => void; options: FilterOption<V>[]; label?: string }) {
  const [ref, slider] = useSlider('[aria-pressed="true"]')
  return (
    <div ref={ref} className="filters" role="group" aria-label={label}>
      {slider}
      {options.map((o) => (
        <button key={o.value} type="button" aria-pressed={value === o.value} onClick={() => onChange(o.value)}>
          {o.label}
          {o.count != null && <span className="count">{o.count}</span>}
        </button>
      ))}
    </div>
  )
}

export interface TabItem<V extends string = string> {
  value: V
  label: ReactNode
  count?: number
  disabled?: boolean
  tip?: string
}

export function Tabs<V extends string>({ value, onChange, items, inset, sub, label }: { value: V; onChange: (v: V) => void; items: TabItem<V>[]; inset?: boolean; sub?: boolean; label?: string }) {
  const [ref, slider] = useSlider('[aria-selected="true"]')
  return (
    <div ref={ref} className={cx('tabs', inset && 'inset', sub && 'sub')} role="tablist" aria-label={label}>
      {slider}
      {items.map((t) => (
        <button key={t.value} role="tab" aria-selected={value === t.value} className={cx('tab', t.disabled && 'disabled')} onClick={() => !t.disabled && onChange(t.value)} data-tip={t.tip}>
          {t.label}
          {t.count != null && <span className="count">{t.count}</span>}
        </button>
      ))}
    </div>
  )
}

export function Field({ label, hint, error, children, aside, className }: { label?: ReactNode; hint?: ReactNode; error?: ReactNode; children?: ReactNode; aside?: ReactNode; className?: string }) {
  return (
    <div className={cx('field', className)}>
      {label && (
        <div className="row">
          <span className="field-label">{label}</span>
          {aside && (
            <span className="field-hint" style={{ marginLeft: 'auto' }}>
              {aside}
            </span>
          )}
        </div>
      )}
      {children}
      {error ? <div className="field-error">{error}</div> : hint ? <div className="field-hint">{hint}</div> : null}
    </div>
  )
}

// ---- select -----------------------------------------------------------------------------------
// A styled dropdown with full keyboard support: arrows, Home/End, Enter/Space,
// Escape, Tab, and type-ahead.

export interface SelectOption<V extends string | number = string> {
  value: V
  label: string
  hint?: string
  disabled?: boolean
}

type SelectOpt<V extends string | number> = V | SelectOption<V>

const normOpt = <V extends string | number>(o: SelectOpt<V>): SelectOption<V> =>
  typeof o === 'object' ? o : { value: o, label: String(o) }

let selectSeq = 0

export function Select<V extends string | number>({
  value,
  onChange,
  options,
  className,
  size,
  disabled,
  style,
  placeholder,
  align = 'start',
  ...rest
}: {
  value: V | null | undefined
  onChange: (v: V) => void
  options: SelectOpt<V>[]
  className?: string
  size?: 'sm' | ''
  disabled?: boolean
  style?: CSSProperties
  placeholder?: string
  align?: 'start' | 'end'
  'aria-label'?: string
  id?: string
}) {
  const opts = options.map(normOpt)
  const btn = useRef<HTMLButtonElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  const [act, setAct] = useState(-1)
  const [pos, setPos] = useState<{ left: number; top: number; minWidth: number } | null>(null)
  const typed = useRef({ q: '', t: 0 })
  const sel = opts.findIndex((o) => String(o.value) === String(value))
  const cur = opts[sel]
  const id = useMemo(() => 'sel' + ++selectSeq, [])
  const enabled = (i: number) => !!opts[i] && !opts[i].disabled
  const step = (from: number, d: number) => {
    let i = from
    for (let n = 0; n < opts.length; n++) {
      i = (i + d + opts.length) % opts.length
      if (enabled(i)) return i
    }
    return from
  }
  const show = () => {
    if (disabled) return
    setAct(sel >= 0 ? sel : step(-1, 1))
    setOpen(true)
  }
  const close = (focus = true) => {
    setOpen(false)
    setPos(null)
    if (focus) btn.current?.focus()
  }
  const choose = (i: number) => {
    if (!enabled(i)) return
    if (String(opts[i].value) !== String(value)) onChange(opts[i].value)
    close()
  }
  useLayoutEffect(() => {
    if (!open || !btn.current || !list.current) return
    const a = btn.current.getBoundingClientRect()
    const m = list.current.getBoundingClientRect()
    const width = Math.max(a.width, m.width)
    let left = align === 'end' ? a.right - width : a.left
    left = clamp(left, 8, window.innerWidth - width - 8)
    let top = a.bottom + 4
    if (top + m.height > window.innerHeight - 8) top = Math.max(8, a.top - m.height - 4)
    setPos({ left, top, minWidth: a.width })
  }, [open, align])
  useEffect(() => {
    if (!open) return
    const h = (e: MouseEvent) => {
      const t = e.target as Node
      if (list.current && !list.current.contains(t) && btn.current && !btn.current.contains(t)) close(false)
    }
    const r = () => close(false)
    document.addEventListener('mousedown', h)
    window.addEventListener('resize', r)
    return () => {
      document.removeEventListener('mousedown', h)
      window.removeEventListener('resize', r)
    }
  }, [open])
  useEffect(() => {
    if (!open || !list.current || act < 0) return
    const el = list.current.children[act] as HTMLElement | undefined
    el?.scrollIntoView({ block: 'nearest' })
  }, [open, act, pos])
  const ahead = (key: string) => {
    const now = Date.now()
    typed.current = { q: (now - typed.current.t < 600 ? typed.current.q : '') + key.toLowerCase(), t: now }
    const q = typed.current.q
    // One letter cycles through matches; a typed word stays on the current match while it fits.
    const from = (open ? act : sel) + (q.length === 1 ? 1 : 0)
    for (let n = 0; n < opts.length; n++) {
      const i = (Math.max(0, from) + n) % opts.length
      if (enabled(i) && opts[i].label.toLowerCase().startsWith(q)) return i
    }
    return -1
  }
  const onKey = (e: React.KeyboardEvent) => {
    if (disabled) return
    const k = e.key
    if (!open) {
      if (k === 'ArrowDown' || k === 'ArrowUp' || k === 'Enter' || k === ' ') {
        e.preventDefault()
        show()
      } else if (k.length === 1 && /\S/.test(k)) {
        const i = ahead(k)
        if (i >= 0 && i !== sel) onChange(opts[i].value)
      }
      return
    }
    if (k === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      close()
    } else if (k === 'Tab') close(false)
    else if (k === 'ArrowDown') {
      e.preventDefault()
      setAct((a) => step(a, 1))
    } else if (k === 'ArrowUp') {
      e.preventDefault()
      setAct((a) => step(a, -1))
    } else if (k === 'Home') {
      e.preventDefault()
      setAct(step(-1, 1))
    } else if (k === 'End') {
      e.preventDefault()
      setAct(step(opts.length, -1))
    } else if (k === 'Enter' || k === ' ') {
      e.preventDefault()
      choose(act)
    } else if (k.length === 1 && /\S/.test(k)) {
      const i = ahead(k)
      if (i >= 0) setAct(i)
    }
  }
  return (
    <>
      <button
        ref={btn}
        type="button"
        className={cx('select', size, className, open && 'open')}
        style={style}
        disabled={disabled}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? id : undefined}
        aria-activedescendant={open && act >= 0 ? id + '-' + act : undefined}
        onClick={(e) => {
          e.stopPropagation()
          if (open) close()
          else show()
        }}
        onKeyDown={onKey}
        {...rest}
      >
        <span className={cx('select-value ellipsis', !cur && 'placeholder')}>{cur ? cur.label : placeholder || 'Choose'}</span>
        <Icon name="chev-d" size={14} className="select-chev" />
      </button>
      {open &&
        createPortal(
          <div
            ref={list}
            id={id}
            role="listbox"
            className="listbox"
            aria-label={rest['aria-label']}
            style={{ left: pos ? pos.left : -9999, top: pos ? pos.top : -9999, minWidth: pos ? pos.minWidth : undefined }}
            onMouseDown={(e) => e.preventDefault()}
          >
            {opts.map((o, i) => (
              <div
                key={String(o.value)}
                id={id + '-' + i}
                role="option"
                aria-selected={i === sel}
                aria-disabled={o.disabled || undefined}
                className={cx('opt', i === act && 'active', i === sel && 'selected', o.disabled && 'disabled')}
                onMouseEnter={() => enabled(i) && setAct(i)}
                onClick={() => choose(i)}
              >
                <span className="grow ellipsis">{o.label}</span>
                {o.hint && <span className="opt-hint">{o.hint}</span>}
                <Icon name="check" size={14} className="opt-check" />
              </div>
            ))}
          </div>,
          document.body,
        )}
    </>
  )
}

// ---- time ---------------------------------------------------------------------------------------

/** "930" → "09:30", "7" → "07:00", "25:00" → null. */
export function normTime(v: string): string | null {
  const d = String(v).replace(/[^\d:]/g, '')
  let h: string
  let m: string
  if (d.includes(':')) [h, m] = d.split(':')
  else if (d.length <= 2) [h, m] = [d, '0']
  else [h, m] = [d.slice(0, d.length - 2), d.slice(-2)]
  const hh = +h
  const mm = +(m || 0)
  if (!d || Number.isNaN(hh) || Number.isNaN(mm) || hh > 23 || mm > 59) return null
  return pad2(hh) + ':' + pad2(mm)
}

export function TimeField({ value, onChange, label, style }: { value: string; onChange: (v: string) => void; label?: string; style?: CSSProperties }) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])
  const ok = normTime(draft) != null
  const commit = () => {
    const t = normTime(draft)
    if (t) {
      setDraft(t)
      if (t !== value) onChange(t)
    }
  }
  const stepBy = (min: number) => {
    const [h, m] = (normTime(draft) || value).split(':').map(Number)
    const t = (((h * 60 + m + min) % 1440) + 1440) % 1440
    const nv = pad2(Math.floor(t / 60)) + ':' + pad2(t % 60)
    setDraft(nv)
    onChange(nv)
  }
  return (
    <input
      className="input num time-field"
      inputMode="numeric"
      value={draft}
      placeholder="HH:MM"
      aria-label={label}
      aria-invalid={!ok}
      style={style}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === 'Enter') commit()
        else if (e.key === 'ArrowUp') {
          e.preventDefault()
          stepBy(e.shiftKey ? 60 : 5)
        } else if (e.key === 'ArrowDown') {
          e.preventDefault()
          stepBy(e.shiftKey ? -60 : -5)
        }
      }}
    />
  )
}

// ---- small pieces -------------------------------------------------------------------------------

export function Search({ value, onChange, placeholder = 'Search', autoFocus, size, style }: { value: string; onChange: (v: string) => void; placeholder?: string; autoFocus?: boolean; size?: 'sm' | ''; style?: CSSProperties }) {
  return (
    <div className="search" style={style}>
      <Icon name="search" size={14} />
      <input className={cx('input', size)} value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} aria-label={placeholder} autoFocus={autoFocus} />
    </div>
  )
}

export function Copy({ text, children, mono }: { text: string; children?: ReactNode; mono?: boolean }) {
  const [done, setDone] = useState(false)
  return (
    <button
      type="button"
      className={cx('copy-inline', mono && 'mono')}
      onClick={(e) => {
        e.stopPropagation()
        void copyText(text)
        setDone(true)
        setTimeout(() => setDone(false), 1200)
      }}
      data-tip={done ? 'Copied' : 'Copy'}
    >
      {children || text}
      <Icon name={done ? 'check' : 'copy'} size={12} />
    </button>
  )
}

export function Meter({ value, max, tone, busy, label }: { value: number; max: number; tone?: 'warn' | 'bad' | ''; busy?: boolean; label?: string }) {
  const pct = max ? clamp((value / max) * 100, 0, 100) : 0
  return (
    <div className={cx('meter', tone, busy && 'busy')} role="progressbar" aria-label={label} aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100}>
      <i style={{ width: pct + '%' }} />
    </div>
  )
}

export function Empty({ title, children, action }: { title: ReactNode; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty">
      <div className="empty-title">{title}</div>
      {children && <div>{children}</div>}
      {action}
    </div>
  )
}

export function Notice({ tone, children, actions }: { tone?: 'warn' | 'bad' | 'ok' | ''; children: ReactNode; actions?: ReactNode }) {
  return (
    <div className={cx('notice', tone)}>
      <p>{children}</p>
      {actions && <div className="row">{actions}</div>}
    </div>
  )
}
