import { create } from 'zustand'

// The panel's theme. Stored per browser; the first visit follows the system.

export type Theme = 'dark' | 'light'

const KEY = 'mcsm-theme'

function initialTheme(): Theme {
  try {
    const stored = localStorage.getItem(KEY)
    if (stored === 'dark' || stored === 'light') return stored
    return window.matchMedia?.('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
  } catch {
    return 'dark'
  }
}

function apply(theme: Theme) {
  const root = document.documentElement
  root.setAttribute('data-theme', theme)
  // Balanced is the one look the panel ships.
  root.setAttribute('data-look', 'balanced')
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', theme === 'dark' ? '#0a0a0a' : '#f5f5f5')
}

interface ThemeState {
  theme: Theme
  toggle: () => void
}

export const useTheme = create<ThemeState>((set, get) => ({
  theme: initialTheme(),
  toggle: () => {
    const theme: Theme = get().theme === 'dark' ? 'light' : 'dark'
    try {
      localStorage.setItem(KEY, theme)
    } catch {
      // Storage blocked: the theme still changes for this visit.
    }
    apply(theme)
    set({ theme })
  },
}))

/** Applies the stored theme before the first render. */
export function bootTheme() {
  apply(useTheme.getState().theme)
}
