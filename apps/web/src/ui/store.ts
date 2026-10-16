import { create } from 'zustand'
import type { ReactNode } from 'react'

// Panel-wide overlays: one of each at a time, opened from anywhere.

export interface ConfirmRequest {
  title: ReactNode
  body?: ReactNode
  confirm?: string
  danger?: boolean
  /** A name to type before a destructive action is allowed. */
  typeToConfirm?: string
  resolve: (ok: boolean) => void
}

export interface StepUpRequest {
  title?: string
  body?: ReactNode
  confirm?: string
  /** Resolves with the password and code once given, or null if cancelled. */
  resolve: (v: StepUpValue | null) => void
}

export interface StepUpValue {
  password: string
  code: string
}

export interface ReauthRequest {
  /** What couldn't be done, phrased to follow "Sign in and …". */
  label: string | null
  /** Runs once signed in again. */
  retry: (() => void) | null
}

interface UiState {
  confirm: ConfirmRequest | null
  stepUp: StepUpRequest | null
  reauth: ReauthRequest | null
  palette: boolean
  navOpen: boolean
  set: (patch: Partial<Omit<UiState, 'set'>>) => void
}

export const useUi = create<UiState>((set) => ({
  confirm: null,
  stepUp: null,
  reauth: null,
  palette: false,
  navOpen: false,
  set: (patch) => set(patch),
}))

export const setUi = (patch: Partial<Omit<UiState, 'set'>>) => useUi.getState().set(patch)

/** Confirmation as a promise. Destructive ones can ask for a name to be typed. */
export function ask(opts: Omit<ConfirmRequest, 'resolve'>): Promise<boolean> {
  return new Promise((resolve) => {
    useUi.getState().confirm?.resolve(false)
    setUi({ confirm: { ...opts, resolve } })
  })
}

/** Asks for the password (and a two-factor code when the account has one)
 *  before something sensitive. */
export function stepUp(opts: Omit<StepUpRequest, 'resolve'> = {}): Promise<StepUpValue | null> {
  return new Promise((resolve) => {
    useUi.getState().stepUp?.resolve(null)
    setUi({ stepUp: { ...opts, resolve } })
  })
}
