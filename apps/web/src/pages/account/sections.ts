// Kept apart from the page so the route can read a section without loading it.

export type AccountSection = 'you' | 'keys' | 'agents'

export function accountSectionOf(s: string | undefined): AccountSection {
  return s === 'keys' || s === 'agents' ? s : 'you'
}
