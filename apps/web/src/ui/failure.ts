import { ApiError, SESSION_ENDED, describeBusy } from '@/lib/api'
import { useConnection } from '@/live/connection'
import { setUi } from './store'
import { toast } from './toasts'

// How a request that didn't land is explained. Every action and every page
// load goes through the same rules, so a failure reads the same everywhere:
// what didn't happen, why, and whether trying again can help.

export type FailureWhy =
  /** The session ended; signing in again fixes it. */
  | 'session'
  /** The panel can't be reached at all. */
  | 'down'
  /** The panel didn't answer in time. */
  | 'timeout'
  /** The panel answered and said no. */
  | 'refused'

export interface Failure {
  why: FailureWhy
  /** One sentence, for a toast body or under a "Couldn't load" title. */
  message: string
  /** Whether trying the same thing again might work. */
  retry: boolean
}

export function failureOf(e: unknown): Failure {
  if (e instanceof ApiError) {
    if (e.code === SESSION_ENDED) return { why: 'session', message: 'Your session ended.', retry: true }
    if (e.status === 502 || e.status === 503) {
      return { why: 'down', message: 'The panel can’t be reached.', retry: true }
    }
    if (e.status === 504 || e.status === 408) {
      return { why: 'timeout', message: 'The panel didn’t answer in time. Nothing changed.', retry: true }
    }
    const busy = describeBusy(e)
    if (busy) return { why: 'refused', message: busy, retry: true }
    return { why: 'refused', message: sentence(e.message), retry: e.retryable }
  }
  if (e instanceof DOMException && (e.name === 'TimeoutError' || e.name === 'AbortError')) {
    return { why: 'timeout', message: 'The panel didn’t answer in time. Nothing changed.', retry: true }
  }
  // fetch rejects with a TypeError when nothing answered at all.
  if (e instanceof TypeError || useConnection.getState().state === 'down') {
    return { why: 'down', message: 'The panel can’t be reached.', retry: true }
  }
  const msg = e instanceof Error ? e.message : String(e)
  return { why: 'refused', message: sentence(msg), retry: false }
}

function sentence(s: string): string {
  const t = (s || 'Something went wrong').trim()
  const first = t.charAt(0).toUpperCase() + t.slice(1)
  return /[.!?]$/.test(first) ? first : first + '.'
}

/**
 * Runs an action the way the panel handles every action: if it fails, a toast
 * says "Couldn't <label>" and why, with Try again when that can help; if the
 * session ended, the person signs in again over the page and the action
 * carries on. Resolves with the result, or undefined if it didn't happen.
 *
 * label follows "Couldn't …" and "Sign in and …": "restart Survival".
 */
export async function act<T>(label: string, fn: () => Promise<T>): Promise<T | undefined> {
  try {
    return await fn()
  } catch (e) {
    const f = failureOf(e)
    const again = () => void act(label, fn)
    if (f.why === 'session') {
      setUi({ reauth: { label, retry: again } })
      return undefined
    }
    toast('bad', `Couldn’t ${label}`, f.message, {
      actions: f.retry ? [{ label: 'Try again', run: again }] : undefined,
    })
    return undefined
  }
}
