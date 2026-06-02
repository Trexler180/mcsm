import { create } from 'zustand'

export type ToastVariant = 'default' | 'success' | 'error' | 'warning'

export interface Toast {
  id: string
  title: string
  description?: string
  variant: ToastVariant
}

interface NotificationState {
  toasts: Toast[]
  add: (t: Omit<Toast, 'id'>) => void
  remove: (id: string) => void
  success: (title: string, description?: string) => void
  error: (title: string, description?: string) => void
  warning: (title: string, description?: string) => void
}

let counter = 0

export const useNotifications = create<NotificationState>()((set, get) => ({
  toasts: [],
  add: (t) => {
    const id = String(++counter)
    set((s) => ({ toasts: [...s.toasts, { ...t, id }] }))
    setTimeout(() => {
      set((s) => ({ toasts: s.toasts.filter((x) => x.id !== id) }))
    }, 4000)
  },
  remove: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
  success: (title, description) =>
    get().add({ title, description, variant: 'success' }),
  error: (title, description) =>
    get().add({ title, description, variant: 'error' }),
  warning: (title, description) =>
    get().add({ title, description, variant: 'warning' }),
}))
