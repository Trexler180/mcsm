import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { MCPConsentServer, MCPConsentView, MCPScope } from '@/lib/types'
import { Btn, Check, Empty, Field, Notice } from '@/ui/controls'
import { failureOf } from '@/ui/failure'
import { PageHead } from '@/ui/page'
import { SkForm } from '@/ui/skeleton'
import { toast } from '@/ui/toasts'

// The screen an AI agent's connection opens in the browser: who's asking,
// what it may do, on which servers, for how long. Only read-only access
// starts ticked; anything that changes a server is added on purpose, and
// nothing can exceed what the agent asked for or what you hold yourself.

/** Read-only capabilities: the safe default and the "Diagnostics only"
 *  preset. Exported so the tests check the list the screen uses. */
export const DIAGNOSTIC_SCOPES: readonly MCPScope[] = ['mcp:servers.read', 'mcp:diagnostics.read', 'mcp:logs.read', 'mcp:metrics.read', 'mcp:audit.read']

/** What the "Server operator" preset adds on top of diagnostics.
 *
 *  `mcp:actions.request` stays out: it isn't a milder operator capability but
 *  the other model, where a person approves each action, and bundling it would
 *  quietly grant a second path to the same actions. The whitelist and console
 *  scopes reach players, and backups always ask; each is ticked on its own. */
export const OPERATOR_SCOPES: readonly MCPScope[] = ['mcp:power.start', 'mcp:power.stop', 'mcp:power.restart', 'mcp:mods.read', 'mcp:mods.install', 'mcp:mods.update', 'mcp:mods.remove']

const OPERATOR_SET = new Set<MCPScope>(OPERATOR_SCOPES)

/** How the screen groups what an agent asks for: by what each means for you. */
export const SCOPE_GROUPS: { title: string; hint?: string; scopes: readonly MCPScope[] }[] = [
  { title: 'Read only', scopes: [...DIAGNOSTIC_SCOPES, 'mcp:mods.read'] },
  { title: 'Sensitive reads', hint: 'What the server itself wrote, its config files, and who plays on it.', scopes: ['mcp:logs.raw', 'mcp:config.read', 'mcp:players.read'] },
  { title: 'Ask first', hint: 'These only file a request; nothing happens until you approve it.', scopes: ['mcp:actions.request', 'mcp:backups.create'] },
  {
    title: 'Without asking',
    hint: 'These happen the moment the agent decides to. There’s no approval step.',
    scopes: ['mcp:power.start', 'mcp:power.stop', 'mcp:power.restart', 'mcp:mods.install', 'mcp:mods.update', 'mcp:mods.remove', 'mcp:players.whitelist', 'mcp:console.run'],
  },
]

/** The requested scopes in their groups; anything this build doesn't know goes last. */
export function groupScopes<T extends { scope: MCPScope }>(requested: T[]): { title: string; hint?: string; items: T[] }[] {
  const known = new Set(SCOPE_GROUPS.flatMap((g) => g.scopes))
  const groups = SCOPE_GROUPS.map((g) => ({ title: g.title, hint: g.hint, items: g.scopes.map((sc) => requested.find((r) => r.scope === sc)).filter((r): r is T => !!r) }))
  const other = requested.filter((r) => !known.has(r.scope))
  if (other.length) groups.push({ title: 'Other', hint: undefined, items: other })
  return groups.filter((g) => g.items.length)
}

/** A preset only ever selects what the client asked for. */
function presetSelection(offered: readonly MCPScope[], preset: readonly MCPScope[]): MCPScope[] {
  return offered.filter((s) => preset.includes(s))
}

// The request id is put in the query string by the API's /authorize redirect.
// It's opaque and single-use; the API validates it.
function requestIDFromLocation(): string {
  return new URLSearchParams(window.location.search).get('request') ?? ''
}

/** Sends the browser where the API says. The target is built server-side from
 *  the stored redirect URI of a validated request, never from this page, which
 *  is what keeps the screen from being an open redirect. An object so tests can
 *  watch the hand-off. */
export const browserNav = {
  assign: (url: string) => window.location.assign(url),
}

function Frame({ children, lede }: { children: React.ReactNode; lede?: string }) {
  return (
    <div className="scroll">
      <div className="page" style={{ maxWidth: 620 }}>
        <PageHead title="Connect an agent" lede={lede} />
        {children}
      </div>
    </div>
  )
}

function ConsentForm({ view }: { view: MCPConsentView }) {
  const offered = view.scopes.map((s) => s.scope)
  // Only the read-only part of the request starts ticked: a client asking for
  // authority to change things needs a person to add it.
  const [scopes, setScopes] = useState<MCPScope[]>(() => presetSelection(offered, DIAGNOSTIC_SCOPES))
  const [serverIDs, setServerIDs] = useState<string[]>([])
  const [days, setDays] = useState(String(view.default_days))
  const [busy, setBusy] = useState<'allow' | 'refuse' | null>(null)

  // A server can only back the capabilities you hold there; the API checks
  // each pair again on approval.
  const eligible = (s: MCPConsentServer) => scopes.every((sc) => s.scopes.includes(sc))

  // Ticking a capability can leave a chosen server unable to back it.
  useEffect(() => {
    setServerIDs((prev) =>
      prev.filter((id) => {
        const srv = view.servers.find((s) => s.id === id)
        return srv ? eligible(srv) : false
      }),
    )
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scopes])

  const decide = async (approve: boolean) => {
    setBusy(approve ? 'allow' : 'refuse')
    try {
      const res = await api.mcp.consent.decide({ request_id: view.request_id, approve, scopes, server_ids: serverIDs, days: Number(days) })
      browserNav.assign(res.redirect_to)
    } catch (e) {
      toast('bad', 'Couldn’t finish connecting', failureOf(e).message)
      setBusy(null)
    }
  }

  const toggleScope = (scope: MCPScope) => setScopes((prev) => (prev.includes(scope) ? prev.filter((s) => s !== scope) : [...prev, scope]))
  const toggleServer = (id: string) => setServerIDs((prev) => (prev.includes(id) ? prev.filter((s) => s !== id) : [...prev, id]))

  // mods.read changes nothing, so it alone doesn't warrant the warning.
  const executesImmediately = scopes.some((s) => OPERATOR_SET.has(s) && s !== 'mcp:mods.read')
  const offeredDiagnostics = presetSelection(offered, DIAGNOSTIC_SCOPES)
  const offeredOperator = presetSelection(offered, OPERATOR_SCOPES)
  const dayCount = Number(days)
  const validDays = Number.isInteger(dayCount) && dayCount >= 1 && dayCount <= view.max_days
  const ready = scopes.length > 0 && serverIDs.length > 0 && validDays

  return (
    <div className="stack" style={{ gap: 22 }}>
      <div>
        <h2 className="section-title" style={{ fontSize: 18 }}>
          <span data-testid="consent-client">{view.client_name}</span> wants to connect
        </h2>
        <p className="t2" style={{ marginTop: 4 }}>
          It will act as you, limited to the servers and capabilities you approve here, and to what you can still do yourself.
        </p>
      </div>

      {view.client_origin === 'dynamic' ? (
        <Notice tone="warn">
          This client registered itself and chose its own name, so treat the name as a claim, not an identity. Allow it only if you just started this connection yourself. It will send you back to <code>{view.redirect_host}</code>, which should be the tool on your own machine.
        </Notice>
      ) : (
        <Notice>
          Registered by an administrator. It will send you back to <code>{view.redirect_host}</code>.
        </Notice>
      )}

      <Field label="What it can do" hint="Only read-only access starts ticked. You can’t grant more than the agent asked for.">
        {/* Presets replace the selection, so Diagnostics only is a way back to safety. */}
        <div className="row" style={{ gap: 8, margin: '4px 0 12px' }}>
          <Btn size="sm" disabled={offeredDiagnostics.length === 0} onClick={() => setScopes(offeredDiagnostics)}>
            Diagnostics only
          </Btn>
          <Btn size="sm" disabled={offeredOperator.length === 0} onClick={() => setScopes([...offeredDiagnostics, ...offeredOperator])}>
            Server operator
          </Btn>
        </div>
        <p className="small t3" style={{ marginBottom: 10 }}>
          Server operator adds power and mod authority. Backups are separate, never part of it, and need your confirmation every time.
        </p>
        <div className="stack" style={{ gap: 18 }}>
          {groupScopes(view.scopes).map((g) => (
            <div key={g.title}>
              <div className="field-label" style={{ marginBottom: 2 }}>
                {g.title}
              </div>
              {g.hint && <p className={g.title === 'Without asking' ? 'small warn' : 'small t3'} style={{ marginBottom: 8 }}>{g.hint}</p>}
              <div className="stack" style={{ gap: 10, marginTop: g.hint ? 0 : 6 }}>
                {g.items.map((s) => (
                  <Check
                    key={s.scope}
                    aria-label={s.title}
                    checked={scopes.includes(s.scope)}
                    onChange={() => toggleScope(s.scope)}
                    label={
                      <span>
                        <span style={{ fontWeight: 500 }}>{s.title}</span>
                        <span className="t2 small" style={{ display: 'block' }}>
                          {s.description}
                        </span>
                      </span>
                    }
                  />
                ))}
              </div>
            </div>
          ))}
        </div>
      </Field>

      <Field label="On these servers" hint="A server you can’t back with every ticked capability is greyed out: you can’t hand over access you don’t have.">
        {view.servers.length === 0 ? (
          <p className="t2">You don’t have the access this request needs on any server, so there’s nothing to hand over. Refuse it.</p>
        ) : (
          <div className="row wrap" style={{ gap: '8px 18px' }}>
            {view.servers.map((s) => (
              <Check key={s.id} aria-label={s.name} checked={serverIDs.includes(s.id)} disabled={!eligible(s)} onChange={() => toggleServer(s.id)} label={s.name} />
            ))}
          </div>
        )}
      </Field>

      <Field label="For" hint={`At most ${view.max_days} days. You can disconnect it sooner from your account at any time.`} error={days && !validDays ? `Between 1 and ${view.max_days} days.` : null}>
        <div className="row" style={{ gap: 8 }}>
          <input className="input num" type="number" min={1} max={view.max_days} value={days} onChange={(e) => setDays(e.target.value)} aria-label="Access expires after" style={{ width: 100 }} />
          <span className="t2">days</span>
        </div>
      </Field>

      {executesImmediately && (
        <Notice tone="warn">
          <strong>These run immediately.</strong> With them ticked, this agent can stop, start, restart and change the mods on the servers you pick, on its own, at any time until this access expires or you disconnect it. You will not be asked again for each action; you’ll find out from the audit log, not a prompt. Allow them only for an agent you’re watching, and prefer Diagnostics only otherwise.
        </Notice>
      )}
      {scopes.includes('mcp:actions.request') && (
        <Notice>Asking to start, stop or restart only files a request. Nothing runs until you approve that request. This is separate from the direct power capabilities above, which don’t ask.</Notice>
      )}

      <div className="row" style={{ gap: 8 }}>
        <Btn variant="primary" disabled={!ready || busy !== null} loading={busy === 'allow'} onClick={() => void decide(true)}>
          Allow access
        </Btn>
        <Btn variant="quiet" disabled={busy !== null} loading={busy === 'refuse'} onClick={() => void decide(false)}>
          Refuse
        </Btn>
      </div>
    </div>
  )
}

export function ConsentPage() {
  // Read once: the id mustn't change under a decision being made.
  const [requestID] = useState(requestIDFromLocation)
  const { data, isLoading, error } = useQuery({
    queryKey: ['mcp-consent', requestID],
    queryFn: ({ signal }) => api.mcp.consent.get(requestID, signal),
    enabled: requestID !== '',
    // Single-use and short-lived: refetching could only show something stale.
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: Infinity,
  })

  if (requestID === '') {
    return (
      <Frame>
        <Empty title="There’s no connection request in this link.">
          Start the connection from your agent (for Claude Code, <code>claude mcp add</code>) and follow the browser window it opens.
        </Empty>
      </Frame>
    )
  }
  if (isLoading) {
    return (
      <Frame>
        <SkForm sections={2} />
      </Frame>
    )
  }
  // Expired, unknown and already answered look the same, on purpose.
  if (error || !data || data.already_decided) {
    return (
      <Frame>
        <Empty title="This connection request is no longer valid.">It may have expired, been used, or been answered in another tab. Run the connect command again to start a new one.</Empty>
      </Frame>
    )
  }
  return (
    <Frame>
      <ConsentForm view={data} />
    </Frame>
  )
}
