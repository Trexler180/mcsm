import { create } from 'zustand'
import { api } from '@/lib/api'
import type { User } from '@/lib/types'

interface AuthState {
  user: User | null
  isAuthenticated: boolean
  isLoading: boolean
  login: (email: string, password: string) => Promise<void>
  logout: () => Promise<void>
  init: () => Promise<void>
}

export const useAuthStore = create<AuthState>((set) => ({
  user: null,
  isAuthenticated: false,
  isLoading: true,

  init: async () => {
    localStorage.removeItem('refresh_token')
    if (!localStorage.getItem('access_token')) {
      try {
        await api.auth.refresh()
      } catch {
        set({ isLoading: false })
        return
      }
    }
    try {
      const user = await api.auth.me()
      set({ user, isAuthenticated: true, isLoading: false })
    } catch {
      localStorage.removeItem('access_token')
      set({ isLoading: false })
    }
  },

  login: async (email, password) => {
    const { access_token, user } = await api.auth.login(email, password)
    localStorage.setItem('access_token', access_token)
    localStorage.removeItem('refresh_token')
    set({ user, isAuthenticated: true })
  },

  logout: async () => {
    await api.auth.logout().catch(() => {})
    localStorage.removeItem('access_token')
    set({ user: null, isAuthenticated: false })
  },
}))
