import { create } from 'zustand'
import { api } from '@/lib/api'
import type { User } from '@/lib/types'

interface LoginResult {
  mfaRequired: boolean
}

interface AuthState {
  user: User | null
  isAuthenticated: boolean
  isLoading: boolean
  login: (
    email: string,
    password: string,
    opts?: { totpCode?: string; recoveryCode?: string },
  ) => Promise<LoginResult>
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

  login: async (email, password, opts) => {
    const res = await api.auth.login(email, password, opts)
    if (res.mfa_required) {
      return { mfaRequired: true }
    }
    localStorage.setItem('access_token', res.access_token!)
    localStorage.removeItem('refresh_token')
    set({ user: res.user!, isAuthenticated: true })
    return { mfaRequired: false }
  },

  logout: async () => {
    await api.auth.logout().catch(() => {})
    localStorage.removeItem('access_token')
    set({ user: null, isAuthenticated: false })
  },
}))
