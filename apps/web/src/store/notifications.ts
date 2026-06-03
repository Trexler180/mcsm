import { create } from 'zustand'

export type ToastVariant = 'default' | 'success' | 'error' | 'warning'

export interface Toast {
  id: string
  title: string
  description?: string
  variant: ToastVariant
  count: number
}

interface NotificationState {
  toasts: Toast[]
  add: (t: Omit<Toast, 'id' | 'count'>) => void
  remove: (id: string) => void
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
    // Reset the auto-dismiss timer for a toast id.
    const arm = (id: string) => {
      const existing = timers.get(id)
      if (existing) clearTimeout(existing)
      timers.set(
        id,
        setTimeout(() => {
          timers.delete(id)
          set((s) => ({ toasts: s.toasts.filter((x) => x.id !== id) }))
        }, TTL),
      )
    }

    // TODO(you): decide when an incoming toast should MERGE into an existing
    // one (bumping its count) vs. appear as a new card. Return the matching
    // toast, or undefined to add a fresh one.
    //   - match on title only?  -> spammy installs collapse aggressively
    //   - match on title+variant? -> keeps errors separate from successes
    //   - never match?           -> original behavior (every event = a card)
    const match = (existing: Toast): boolean =>
      existing.title === t.title && existing.variant === t.variant

    const dup = get().toasts.find(match)
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
  success: (title, description) =>
    get().add({ title, description, variant: 'success' }),
  error: (title, description) =>
    get().add({ title, description, variant: 'error' }),
  warning: (title, description) =>
    get().add({ title, description, variant: 'warning' }),
}))
