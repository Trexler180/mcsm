import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { clsx } from 'clsx'
import { AlertTriangle, Check, Gamepad2, Loader2, User } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { api } from '@/lib/api'
import type { BedrockIdentity, GeyserInfo } from '@/lib/types'

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
 * is separate from the markup below so the dialogs that use it can lay their
 * forms out differently while behaving identically.
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

function EditionToggle({
  edition,
  onChange,
}: {
  edition: PlayerEdition
  onChange: (e: PlayerEdition) => void
}) {
  const options: { key: PlayerEdition; label: string; icon: React.ReactNode }[] = [
    { key: 'java', label: 'Java', icon: <User className="h-3.5 w-3.5" /> },
    { key: 'bedrock', label: 'Bedrock', icon: <Gamepad2 className="h-3.5 w-3.5" /> },
  ]
  return (
    <div role="tablist" className="flex gap-1 rounded-lg bg-surface-2 p-1">
      {options.map((o) => (
        <button
          key={o.key}
          type="button"
          role="tab"
          aria-selected={edition === o.key}
          onClick={() => onChange(o.key)}
          className={clsx(
            'flex flex-1 items-center justify-center gap-1.5 rounded-md px-3 py-1 text-xs font-medium transition-colors',
            edition === o.key
              ? 'bg-surface text-text-primary shadow-sm'
              : 'text-text-secondary hover:text-text-primary',
          )}
        >
          {o.icon} {o.label}
        </button>
      ))}
    </div>
  )
}

/**
 * The resolved-as readout. It shows the exact name and UUID that will be
 * written, so a wrong edition guess or a mistyped gamertag is visible before
 * anything is committed rather than after the player fails to connect.
 */
function ResolvedIdentity({ identity }: { identity: BedrockIdentity }) {
  return (
    <div className="rounded-md border border-cyan-800/50 bg-cyan-900/20 px-3 py-2">
      <p className="flex items-center gap-1.5 text-xs font-medium text-cyan-200">
        <Check className="h-3.5 w-3.5" /> {identity.gamertag}
      </p>
      <dl className="mt-1.5 space-y-0.5 text-[11px] text-text-secondary">
        <div className="flex gap-1.5">
          <dt className="w-14 flex-shrink-0">Joins as</dt>
          <dd className="truncate font-mono text-text-primary">{identity.name}</dd>
        </div>
        <div className="flex gap-1.5">
          <dt className="w-14 flex-shrink-0">UUID</dt>
          <dd className="truncate font-mono">{identity.uuid}</dd>
        </div>
        <div className="flex gap-1.5">
          <dt className="w-14 flex-shrink-0">XUID</dt>
          <dd className="truncate font-mono">{identity.xuid}</dd>
        </div>
      </dl>
    </div>
  )
}

/**
 * PlayerNameField renders the input, the edition toggle where it applies, and
 * whatever the current input resolves to.
 */
export function PlayerNameField({
  state,
  onSubmit,
  autoFocus,
  label = 'Player name',
}: {
  state: PlayerNameState
  onSubmit?: () => void
  autoFocus?: boolean
  label?: string
}) {
  const {
    input,
    setInput,
    edition,
    chooseEdition,
    bedrockAvailable,
    prefix,
    trimmed,
    resolved,
    resolving,
    resolveError,
    selection,
  } = state

  const bedrock = edition === 'bedrock'
  const javaMalformed = !bedrock && trimmed !== '' && !selection.valid
  // Geyser bridges the connection but does not create Bedrock identities — only
  // Floodgate does. Without it there is nothing to whitelist ahead of a first
  // join, so say so rather than leaving an admin hunting for a missing control.
  const geyserWithoutFloodgate = !!state.geyserOnly

  return (
    <div className="space-y-2">
      {bedrockAvailable && <EditionToggle edition={edition} onChange={chooseEdition} />}

      {geyserWithoutFloodgate && (
        <p className="rounded-md border border-border bg-surface-2/50 px-3 py-2 text-xs text-text-secondary">
          This server runs Geyser without Floodgate, so Bedrock players sign in
          with their own Java account. Whitelist them by that Java name.
        </p>
      )}

      <div>
        <label className="mb-1 block text-xs font-medium text-text-secondary">{label}</label>
        <Input
          placeholder={bedrock ? 'Xbox gamertag, e.g. Cool Guy 42' : 'e.g. Notch'}
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && onSubmit) onSubmit()
          }}
          autoFocus={autoFocus}
        />

        {javaMalformed && (
          <p className="mt-1 text-xs text-red-400">
            Java names are 1–16 characters: letters, digits, underscore.
            {bedrockAvailable ? ' For a Bedrock player, switch to Bedrock above.' : ''}
          </p>
        )}

        {bedrock && (
          <div className="mt-2">
            {resolving && (
              <p className="flex items-center gap-1.5 text-xs text-text-secondary">
                <Loader2 className="h-3.5 w-3.5 animate-spin" /> Looking up gamertag…
              </p>
            )}
            {!resolving && resolveError && (
              <p className="flex items-start gap-1.5 text-xs text-red-400">
                <AlertTriangle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
                <span>{resolveError}</span>
              </p>
            )}
            {!resolving && resolved && <ResolvedIdentity identity={resolved} />}
            {!resolving && !resolved && !resolveError && trimmed === '' && (
              <p className="text-xs text-text-secondary">
                Enter the player&apos;s Xbox gamertag
                {prefix ? ` — or the "${prefix}" name they show up as in game` : ''}.
              </p>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

/**
 * Caveats worth showing next to a Bedrock whitelist action. Neither is
 * detectable from the panel, and both produce the same symptom — the player is
 * refused despite appearing on the whitelist — so they are stated up front
 * rather than left to be discovered.
 */
export function BedrockWhitelistNotes({
  edition,
  whitelistEnabled,
}: {
  edition: PlayerEdition
  whitelistEnabled: boolean | undefined
}) {
  const notes: string[] = []
  if (whitelistEnabled === false) {
    notes.push(
      'The whitelist is switched off for this server, so this entry will not restrict anyone until you enable white-list in Options.',
    )
  }
  if (edition === 'bedrock') {
    notes.push(
      'If this player has linked a Java account in Floodgate, they connect under that Java name instead — whitelist them as Java.',
    )
  }
  if (notes.length === 0) return null

  return (
    <div className="space-y-1.5">
      {notes.map((n) => (
        <p
          key={n}
          className="flex items-start gap-1.5 rounded-md border border-border bg-surface-2/50 px-3 py-2 text-xs text-text-secondary"
        >
          <AlertTriangle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-amber-400" />
          <span>{n}</span>
        </p>
      ))}
    </div>
  )
}
