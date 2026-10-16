import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { GeyserInfo } from '@/lib/types'


// A player-name capture that knows the difference between a Java account and a
// Bedrock one. Shared by every surface that whitelists by name so they cannot
// drift apart.
//
// The two editions are not interchangeable. A Java name is applied by console
// command and the server resolves it against Mojang. A Bedrock player has no
// Mojang account, so their entry has to carry the UUID Floodgate mints from
// their Xbox XUID — which means resolving the gamertag before anything is
// written, and telling the admin plainly when that fails.

export type PlayerEdition = 'java' | 'bedrock'

const JAVA_NAME_RE = /^[A-Za-z0-9_]{1,16}$/

// Long enough that ordinary typing doesn't reach a third-party API on every
// keystroke, short enough that the preview still feels live.
const RESOLVE_DEBOUNCE_MS = 500

/** What the parent needs in order to submit a whitelist action. */
export interface PlayerNameSelection {
  edition: PlayerEdition
  /** The name to send. For Bedrock this is the derived Floodgate username. */
  name: string
  /** Set for Bedrock so the agent re-resolves rather than trusting this client. */
  gamertag?: string
  /** False while the input is empty, malformed, resolving, or unresolvable. */
  valid: boolean
}

/**
 * looksBedrock guesses the edition from what has been typed. The Floodgate
 * prefix is the main signal; a space is a second one, since Java names cannot
 * contain spaces but Xbox gamertags can.
 *
 * This is only ever a default. An empty or alphanumeric Floodgate prefix makes
 * the guess unreliable — `Bobby` on a server whose prefix is `B` reads equally
 * well as either edition — so the toggle stays visible and the admin can
 * correct it before submitting.
 */
function looksBedrock(input: string, prefix: string | undefined): boolean {
  if (prefix && input.startsWith(prefix)) return true
  return input.includes(' ')
}

/**
 * usePlayerName owns the edition choice and the debounced gamertag lookup. It
 * holds no markup, so each form lays itself out while behaving identically.
 */
export function usePlayerName(serverId: string, meta: GeyserInfo | undefined) {
  const [input, setInput] = useState('')
  const [edition, setEdition] = useState<PlayerEdition>('java')
  // Once the admin picks an edition themselves, stop overriding them.
  const [editionPinned, setEditionPinned] = useState(false)
  const [debounced, setDebounced] = useState('')

  // Bedrock identities only exist where Floodgate does. Geyser on its own
  // bridges the connection but leaves players signing in with a real Java
  // account, so there is no Floodgate UUID to mint for them.
  const bedrockAvailable = !!meta?.floodgate
  // Worth calling out explicitly: this is the one setup where a Bedrock player
  // is real but the Bedrock control is deliberately absent.
  const geyserOnly = !!meta?.geyser && !meta?.floodgate
  const prefix = meta?.prefix

  const trimmed = input.trim()

  useEffect(() => {
    const t = setTimeout(() => setDebounced(trimmed), RESOLVE_DEBOUNCE_MS)
    return () => clearTimeout(t)
  }, [trimmed])

  // Re-guess the edition as the admin types, until they override it.
  useEffect(() => {
    if (editionPinned || !bedrockAvailable) return
    setEdition(looksBedrock(trimmed, prefix) ? 'bedrock' : 'java')
  }, [trimmed, prefix, editionPinned, bedrockAvailable])

  const lookingUpBedrock = edition === 'bedrock' && bedrockAvailable && debounced.length > 0

  const {
    data: identity,
    error,
    isFetching,
  } = useQuery({
    queryKey: ['bedrock-resolve', serverId, debounced],
    queryFn: () => api.players.resolveBedrock(serverId, debounced),
    enabled: lookingUpBedrock,
    // A gamertag's XUID never changes, and the agent memoises too; retrying a
    // rejected gamertag would just spend another third-party call to be told
    // the same thing.
    retry: false,
    staleTime: 10 * 60_000,
  })

  // While the debounce is still catching up the query result belongs to the
  // previous input, so treat it as pending rather than showing a stale answer.
  const settled = debounced === trimmed
  const resolving = lookingUpBedrock && (isFetching || !settled)

  const resolved = lookingUpBedrock && settled && !isFetching ? (identity ?? null) : null
  const resolveError =
    lookingUpBedrock && settled && !isFetching && error ? (error as Error).message : null

  const selection: PlayerNameSelection = useMemo(() => {
    if (edition === 'bedrock') {
      return {
        edition,
        name: resolved?.name ?? '',
        gamertag: resolved?.gamertag ?? trimmed,
        valid: !!resolved,
      }
    }
    return { edition, name: trimmed, valid: JAVA_NAME_RE.test(trimmed) }
  }, [edition, resolved, trimmed])

  const reset = () => {
    setInput('')
    setDebounced('')
    setEdition('java')
    setEditionPinned(false)
  }

  const chooseEdition = (e: PlayerEdition) => {
    setEdition(e)
    setEditionPinned(true)
  }

  return {
    input,
    setInput,
    edition,
    chooseEdition,
    bedrockAvailable,
    geyserOnly,
    prefix,
    trimmed,
    resolved,
    resolving,
    resolveError,
    selection,
    reset,
  }
}

export type PlayerNameState = ReturnType<typeof usePlayerName>
