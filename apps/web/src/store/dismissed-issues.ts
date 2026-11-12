import { create } from 'zustand'

// Home's "Needs attention" items someone has dismissed. Kept per browser and
// per user. Each entry records the issue's key and the state it was about (its
// fingerprint): the issue stays hidden while that state holds, and comes back
// when it changes (a new backup failure) or after it clears and recurs.

const KEY = 'mcsm-dismissed-issues'

/** Issue key → the fingerprint it was dismissed at. */
export type Dismissed = Record<string, string>

interface Dismissable {
  key: string
  fp?: string
}

/** Splits issues into the ones to show and the ones dismissed. */
export function splitDismissed<T extends Dismissable>(issues: T[], dismissed: Dismissed): { shown: T[]; hidden: T[] } {
  const shown: T[] = []
  const hidden: T[] = []
  for (const it of issues) (dismissed[it.key] === (it.fp ?? '') ? hidden : shown).push(it)
  return { shown, hidden }
}

/** Drops dismissals for issues that have cleared, so they show again if they
 *  recur. `settled` says whether a key's source has loaded: an issue missing
 *  because its data hasn't arrived yet hasn't cleared. Returns the same object
 *  when nothing changed. */
export function pruneDismissed(dismissed: Dismissed, present: Dismissable[], settled: (key: string) => boolean = () => true): Dismissed {
  const keys = new Set(present.map((i) => i.key))
  let next: Dismissed | null = null
  for (const k of Object.keys(dismissed)) {
    if (keys.has(k) || !settled(k)) continue
    next ??= { ...dismissed }
    delete next[k]
  }
  return next ?? dismissed
}

type ByUser = Record<string, Dismissed>

function load(): ByUser {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) || '{}') as unknown
    return v && typeof v === 'object' && !Array.isArray(v) ? (v as ByUser) : {}
  } catch {
    return {}
  }
}

function save(all: ByUser) {
  try {
    localStorage.setItem(KEY, JSON.stringify(all))
  } catch {
    // Storage blocked: dismissals last for this visit.
  }
}

const EMPTY: Dismissed = {}

interface DismissedState {
  all: ByUser
  dismiss: (user: string, it: Dismissable) => void
  restore: (user: string, keys: string[]) => void
  set: (user: string, next: Dismissed) => void
}

export const useDismissedIssues = create<DismissedState>((set, get) => {
  const put = (user: string, next: Dismissed) => {
    const all = { ...get().all }
    if (Object.keys(next).length) all[user] = next
    else delete all[user]
    save(all)
    set({ all })
  }
  return {
    all: load(),
    dismiss: (user, it) => put(user, { ...(get().all[user] ?? EMPTY), [it.key]: it.fp ?? '' }),
    restore: (user, keys) => {
      const next = { ...(get().all[user] ?? EMPTY) }
      for (const k of keys) delete next[k]
      put(user, next)
    },
    set: (user, next) => {
      if (next !== (get().all[user] ?? EMPTY)) put(user, next)
    },
  }
})

/** The current user's dismissals. */
export const dismissedFor = (user: string | undefined) => (s: DismissedState) => (user && s.all[user]) || EMPTY
