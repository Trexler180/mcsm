import { create } from 'zustand'
import { api, clearSession, hasAccessToken, setAccessToken } from '@/lib/api'
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
    // The access token is kept in memory, so a fresh page has none: the
    // refresh cookie says whether there is still a session to pick up.
    if (!hasAccessToken()) {
      try {
        await api.auth.refresh()
      } catch {
        // Signed out. Also drops a token an older panel left in storage.
        clearSession()
        set({ isLoading: false })
        return
      }
    }
    try {
      const user = await api.auth.me()
      set({ user, isAuthenticated: true, isLoading: false })
    } catch {
      clearSession()
      set({ isLoading: false })
    }
  },

  login: async (email, password, opts) => {
    const res = await api.auth.login(email, password, opts)
    if (res.mfa_required) {
      return { mfaRequired: true }
    }
    setAccessToken(res.access_token!)
    set({ user: res.user!, isAuthenticated: true })
    return { mfaRequired: false }
  },

  logout: async () => {
    await api.auth.logout().catch(() => {})
    clearSession()
    set({ user: null, isAuthenticated: false })
  },
}))
