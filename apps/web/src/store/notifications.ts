import { create } from 'zustand'

export type ToastVariant = 'default' | 'success' | 'error' | 'warning'

/** A single button rendered on the toast. The handler decides the toast's fate:
 *  resolving removes it, so the common "do the thing, then show the outcome"
 *  flow reads as one call. */
export interface ToastAction {
  label: string
  onClick: () => void | Promise<void>
}

export interface Toast {
  id: string
  title: string
  description?: string
  variant: ToastVariant
  count: number
  action?: ToastAction
  /** Suppresses the auto-dismiss timer. A toast that asks something of the user
   *  must not disappear four seconds later — they may be mid-sentence, or not
   *  at the keyboard at all. */
  sticky?: boolean
  /** Set while the action is running, so the button can show progress and not
   *  be pressed twice. */
  busy?: boolean
}

interface NotificationState {
  toasts: Toast[]
  add: (t: Omit<Toast, 'id' | 'count'>) => void
  remove: (id: string) => void
  runAction: (id: string) => Promise<void>
  success: (title: string, description?: string) => void
  error: (title: string, description?: string) => void
  warning: (title: string, description?: string) => void
}

let counter = 0
const TTL = 4000
const timers = new Map<string, ReturnType<typeof setTimeout>>()

export const useNotifications = create<NotificationState>()((set, get) => ({
  toasts: [],
  add: (t) => {
    // Reset the auto-dismiss timer for a toast id. Sticky toasts have none.
    const arm = (id: string) => {
      const existing = timers.get(id)
      if (existing) clearTimeout(existing)
      timers.delete(id)
      if (t.sticky) return
      timers.set(
        id,
        setTimeout(() => {
          timers.delete(id)
          set((s) => ({ toasts: s.toasts.filter((x) => x.id !== id) }))
        }, TTL),
      )
    }

    // Merge rule: same title AND variant collapses into one card with a
    // bumped count, so spammy repeats (bulk installs) stack while an error
    // never merges into a success for the same action. A toast carrying an
    // action is exempt: two prompts that happen to share a title are two
    // separate decisions, and collapsing them would silently drop one.
    const match = (existing: Toast): boolean =>
      !existing.action &&
      existing.title === t.title &&
      existing.variant === t.variant

    const dup = t.action ? undefined : get().toasts.find(match)
    if (dup) {
      set((s) => ({
        toasts: s.toasts.map((x) =>
          x.id === dup.id ? { ...x, count: x.count + 1 } : x,
        ),
      }))
      arm(dup.id)
      return
    }

    const id = String(++counter)
    set((s) => ({ toasts: [...s.toasts, { ...t, id, count: 1 }] }))
    arm(id)
  },
  remove: (id) => {
    const existing = timers.get(id)
    if (existing) clearTimeout(existing)
    timers.delete(id)
    set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) }))
  },
  // Runs a toast's action, marking it busy for the duration and dismissing it
  // afterwards. The handler reports its own outcome (typically by raising a
  // success or error toast), so a failure leaves the user informed rather than
  // staring at a prompt that did nothing.
  runAction: async (id) => {
    const toast = get().toasts.find((t) => t.id === id)
    if (!toast?.action || toast.busy) return
    set((s) => ({
      toasts: s.toasts.map((t) => (t.id === id ? { ...t, busy: true } : t)),
    }))
    try {
      await toast.action.onClick()
    } finally {
      get().remove(id)
    }
  },
  success: (title, description) =>
    get().add({ title, description, variant: 'success' }),
  error: (title, description) =>
    get().add({ title, description, variant: 'error' }),
  warning: (title, description) =>
    get().add({ title, description, variant: 'warning' }),
}))
