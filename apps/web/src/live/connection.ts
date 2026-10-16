import { create } from 'zustand'

// Whether live updates are flowing. The banner, the dimmed numbers and the
// "Couldn't load" wording all read this.

export type ConnState =
  /** Live updates are flowing. */
  | 'up'
  /** The live stream dropped; it is being re-established with backoff. */
  | 'reconnecting'
  /** The panel itself isn't answering after several tries, so pages and
   *  actions will fail too until it's back. */
  | 'down'

export interface Connection {
  state: ConnState
  /** When updates stopped: everything on screen is from then. */
  since: number | null
  /** Tries since updates stopped. */
  attempt: number
  /** When the next try happens. */
  nextAt: number | null
  /** When the last try happened. */
  checkedAt: number | null
}

export const INITIAL_CONNECTION: Connection = { state: 'up', since: null, attempt: 0, nextAt: null, checkedAt: null }

interface ConnectionStore extends Connection {
  /** Tries to reconnect now instead of waiting. Set by the live client. */
  tryNow: () => void
}

export const useConnection = create<ConnectionStore>(() => ({ ...INITIAL_CONNECTION, tryNow: () => {} }))
