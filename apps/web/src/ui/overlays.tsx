import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from 'react'
import { createPortal } from 'react-dom'
import type { ServerPermission } from '@/lib/types'
import { Btn, Field, IconBtn } from './controls'
import { Icon, type IconName } from './icon'
import { deniedTip, useCan } from './perm'
import { setUi, useUi } from './store'
import { clamp, cx } from './util'

export function useEscape(open: boolean, onClose?: () => void) {
  useEffect(() => {
    if (!open) return
    const h = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onClose?.()
      }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [open, onClose])
}

/** Focuses the first field when an overlay opens, and gives focus back to
 *  whatever had it when it closes. */
export function useFocusFirst(open: boolean, ref: RefObject<HTMLElement | null>) {
  useEffect(() => {
    if (!open || !ref.current) return
    const prev = document.activeElement as HTMLElement | null
    const el =
      ref.current.querySelector<HTMLElement>('[autofocus], input:not([type=checkbox]):not([type=range]):not([readonly]), textarea:not([readonly])') ||
      ref.current
    const t = setTimeout(() => el?.focus({ preventScroll: true }), 20)
    return () => {
      clearTimeout(t)
      prev?.focus?.()
    }
  }, [open, ref])
}

export function Dialog({
  open,
  onClose,
  title,
  children,
  foot,
  size,
}: {
  open: boolean
  onClose: () => void
  title: ReactNode
  children?: ReactNode
  foot?: ReactNode
  size?: 'wide' | 'narrow' | ''
}) {
  const ref = useRef<HTMLDivElement>(null)
  useEscape(open, onClose)
  useFocusFirst(open, ref)
  if (!open) return null
  return createPortal(
    <div
      className="scrim"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div ref={ref} tabIndex={-1} style={{ outline: 'none' }} className={cx('dialog', size)} role="dialog" aria-modal="true" aria-label={typeof title === 'string' ? title : undefined}>
        <div className="dialog-head">
          <h2 className="dialog-title grow">{title}</h2>
          <IconBtn icon="x" label="Close" onClick={onClose} />
        </div>
        <div className="dialog-body">{children}</div>
        {foot && <div className="dialog-foot">{foot}</div>}
      </div>
    </div>,
    document.body,
  )
}

export function Sheet({
  open,
  onClose,
  title,
  sub,
  children,
  foot,
  wide,
  icon,
}: {
  open: boolean
  onClose: () => void
  title: ReactNode
  sub?: ReactNode
  children?: ReactNode
  foot?: ReactNode
  wide?: boolean
  icon?: ReactNode
}) {
  const ref = useRef<HTMLElement>(null)
  useEscape(open, onClose)
  useFocusFirst(open, ref)
  if (!open) return null
  return createPortal(
    <>
      <div className="sheet-scrim" onClick={onClose} />
      <aside ref={ref} tabIndex={-1} style={{ outline: 'none' }} className={cx('sheet', wide && 'wide')} role="dialog" aria-modal="true" aria-label={typeof title === 'string' ? title : undefined}>
        <div className="sheet-head">
          {icon}
          <div className="grow">
            <h2 className="dialog-title" style={{ fontSize: 20 }}>
              {title}
            </h2>
            {sub && (
              <div className="t3 small" style={{ marginTop: 3 }}>
                {sub}
              </div>
            )}
          </div>
          <IconBtn icon="x" label="Close" onClick={onClose} />
        </div>
        <div className="sheet-body">{children}</div>
        {foot && <div className="sheet-foot">{foot}</div>}
      </aside>
    </>,
    document.body,
  )
}

/** The panel's confirmation dialog, driven by ask(). */
export function ConfirmHost() {
  const c = useUi((s) => s.confirm)
  const [typed, setTyped] = useState('')
  useEffect(() => setTyped(''), [c])
  if (!c) return null
  const close = (v: boolean) => {
    c.resolve(v)
    setUi({ confirm: null })
  }
  const ok = !c.typeToConfirm || typed === c.typeToConfirm
  return (
    <Dialog
      open
      onClose={() => close(false)}
      title={c.title}
      foot={
        <>
          <Btn variant="quiet" onClick={() => close(false)}>
            Cancel
          </Btn>
          <Btn variant={c.danger ? 'danger primary' : 'primary'} disabled={!ok} onClick={() => close(true)}>
            {c.confirm || 'OK'}
          </Btn>
        </>
      }
    >
      {c.body && <p className="t2">{c.body}</p>}
      {c.typeToConfirm && (
        <div style={{ marginTop: 14 }}>
          <Field
            label={
              <>
                Type <b>{c.typeToConfirm}</b> to confirm
              </>
            }
          >
            <input
              className="input"
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              autoFocus
              onKeyDown={(e) => {
                if (e.key === 'Enter' && ok) close(true)
              }}
            />
          </Field>
        </div>
      )}
    </Dialog>
  )
}

// ---- menus ----------------------------------------------------------------------------------

export function Menu({
  anchor,
  open,
  onClose,
  children,
  align = 'end',
  width,
  className,
}: {
  anchor: RefObject<HTMLElement | null>
  open: boolean
  onClose: () => void
  children: ReactNode
  align?: 'start' | 'end'
  width?: number
  className?: string
}) {
  const ref = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)
  useEscape(open, onClose)
  useLayoutEffect(() => {
    if (!open) {
      setPos(null)
      return
    }
    if (!anchor.current) return
    const a = anchor.current.getBoundingClientRect()
    const m = ref.current ? ref.current.getBoundingClientRect() : { width: width || 200, height: 200 }
    let left = align === 'end' ? a.right - m.width : a.left
    left = clamp(left, 8, window.innerWidth - m.width - 8)
    let top = a.bottom + 4
    if (top + m.height > window.innerHeight - 8) top = Math.max(8, a.top - m.height - 4)
    setPos({ left, top })
  }, [open, anchor, align, width])
  useEffect(() => {
    if (!open) return
    const h = (e: MouseEvent) => {
      const t = e.target as Node
      if (ref.current && !ref.current.contains(t) && anchor.current && !anchor.current.contains(t)) onClose()
    }
    const r = () => onClose()
    const t = setTimeout(() => document.addEventListener('mousedown', h), 0)
    window.addEventListener('resize', r)
    return () => {
      clearTimeout(t)
      document.removeEventListener('mousedown', h)
      window.removeEventListener('resize', r)
    }
  }, [open, anchor, onClose])
  useEffect(() => {
    if (open && pos && ref.current) ref.current.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus()
  }, [open, pos])
  if (!open) return null
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return
    e.preventDefault()
    const items = [...(ref.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])]
    const i = items.indexOf(document.activeElement as HTMLButtonElement)
    items[(i + (e.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length]?.focus()
  }
  return createPortal(
    <div
      ref={ref}
      className={cx('menu', className)}
      role="menu"
      style={{ left: pos ? pos.left : -9999, top: pos ? pos.top : -9999, width }}
      onKeyDown={onKey}
      onClick={(e) => e.stopPropagation()}
    >
      {children}
    </div>,
    document.body,
  )
}

export function MenuItem({
  icon,
  children,
  onClick,
  danger,
  disabled,
  hint,
  need,
  close,
}: {
  /** null keeps the icon's space, so labels line up with items that have one. */
  icon?: IconName | null
  children: ReactNode
  onClick?: () => void
  danger?: boolean
  disabled?: boolean
  hint?: ReactNode
  need?: ServerPermission
  close?: () => void
}) {
  const allowed = useCan()
  const denied = !!need && !allowed(need)
  return (
    <button
      type="button"
      role="menuitem"
      className={cx('menu-item', danger && 'danger')}
      disabled={disabled || denied}
      data-tip={denied && need ? deniedTip(need) : undefined}
      onClick={(e) => {
        e.stopPropagation()
        close?.()
        onClick?.()
      }}
    >
      {icon ? <Icon name={icon} size={15} /> : icon === null ? <span style={{ width: 15, flex: 'none' }} /> : null}
      <span className="grow ellipsis">{children}</span>
      {hint && <span className="hint">{hint}</span>}
    </button>
  )
}

export const MenuSep = () => <div className="menu-sep" />
export const MenuLabel = ({ children }: { children: ReactNode }) => <div className="menu-label">{children}</div>

export function MenuButton({
  icon = 'more',
  label = 'More',
  children,
  items,
  align,
  width,
  variant = 'quiet',
  size = 'sm',
  className,
}: {
  icon?: IconName
  label?: string
  children?: ReactNode
  items: (close: () => void) => ReactNode
  align?: 'start' | 'end'
  width?: number
  variant?: string
  size?: 'sm' | ''
  className?: string
}) {
  const ref = useRef<HTMLSpanElement>(null)
  const [open, setOpen] = useState(false)
  const close = useCallback(() => setOpen(false), [])
  const toggle = (e: React.MouseEvent) => {
    e.stopPropagation()
    setOpen((o) => !o)
  }
  return (
    <>
      <span ref={ref} style={{ display: 'inline-flex' }} className={className}>
        {children ? (
          <Btn variant={variant} size={size} iconRight="chev-d" onClick={toggle} aria-haspopup="menu" aria-expanded={open}>
            {children}
          </Btn>
        ) : (
          <IconBtn icon={icon} label={label} variant={variant} size={size} onClick={toggle} aria-haspopup="menu" aria-expanded={open} />
        )}
      </span>
      <Menu anchor={ref} open={open} onClose={close} align={align} width={width}>
        {items(close)}
      </Menu>
    </>
  )
}

// ---- tooltips -------------------------------------------------------------------------------
// Anything with data-tip gets a tooltip, on hover or keyboard focus.

export function TipHost() {
  const [tip, setTip] = useState<{ text: string; x: number; y: number; b: number } | null>(null)
  const ref = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined
    const show = (el: Element) => {
      const text = el.getAttribute('data-tip')
      if (!text) return
      const r = el.getBoundingClientRect()
      clearTimeout(timer)
      timer = setTimeout(() => setTip({ text, x: r.left + r.width / 2, y: r.top, b: r.bottom }), 350)
    }
    const target = (e: Event) => (e.target instanceof Element ? e.target.closest('[data-tip]') : null)
    const over = (e: MouseEvent) => {
      const el = target(e)
      if (el) show(el)
      else {
        clearTimeout(timer)
        setTip(null)
      }
    }
    const hide = () => {
      clearTimeout(timer)
      setTip(null)
    }
    const focus = (e: FocusEvent) => {
      const el = target(e)
      if (el && el.matches(':focus-visible')) show(el)
    }
    document.addEventListener('mouseover', over)
    document.addEventListener('focusin', focus)
    document.addEventListener('focusout', hide)
    document.addEventListener('mousedown', hide)
    window.addEventListener('scroll', hide, true)
    return () => {
      clearTimeout(timer)
      document.removeEventListener('mouseover', over)
      document.removeEventListener('focusin', focus)
      document.removeEventListener('focusout', hide)
      document.removeEventListener('mousedown', hide)
      window.removeEventListener('scroll', hide, true)
    }
  }, [])
  useLayoutEffect(() => {
    if (!tip || !ref.current) {
      setPos(null)
      return
    }
    const w = ref.current.offsetWidth
    const h = ref.current.offsetHeight
    let top = tip.y - h - 7
    if (top < 6) top = tip.b + 7
    setPos({ left: clamp(tip.x - w / 2, 6, window.innerWidth - w - 6), top })
  }, [tip])
  if (!tip) return null
  return createPortal(
    <div ref={ref} className="tip" role="tooltip" style={{ left: pos ? pos.left : -999, top: pos ? pos.top : -999 }}>
      {tip.text}
    </div>,
    document.body,
  )
}
