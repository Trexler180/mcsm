import { useEffect, useState } from 'react'
import { useQuery, useMutation } from '@tanstack/react-query'
import { AlertTriangle, Plug, ShieldAlert } from 'lucide-react'
import { Header } from '@/components/layout/header'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'
import { useNotifications } from '@/store/notifications'
import type { MCPConsentServer, MCPConsentView, MCPScope } from '@/lib/types'

/** Read-only capabilities. The safe default and the "Diagnostics only" preset.
 *  Exported so the tests assert against the same list the UI uses rather than
 *  a copy that could drift away from it. */
export const DIAGNOSTIC_SCOPES: readonly MCPScope[] = [
  'mcp:servers.read',
  'mcp:diagnostics.read',
  'mcp:logs.read',
  'mcp:metrics.read',
  'mcp:audit.read',
]

/** Capabilities the "Server operator" preset adds on top of diagnostics.
 *
 *  `mcp:actions.request` is deliberately absent. It is not a milder operator
 *  capability — it is the other model entirely, where a human approves each
 *  action — so bundling it into a preset would quietly grant a second, differently
 *  gated path to the same lifecycle actions. A human can still tick it alone.
 *
 *  `mcp:players.whitelist` and `mcp:console.run` are absent for a different
 *  reason: they reach players rather than the server's software, and adding
 *  them here would silently widen what an existing one-click preset grants for
 *  everyone who has used it before. They are ticked individually, like
 *  `mcp:backups.create`. */
export const OPERATOR_SCOPES: readonly MCPScope[] = [
  'mcp:power.start',
  'mcp:power.stop',
  'mcp:power.restart',
  'mcp:mods.read',
  'mcp:mods.install',
  'mcp:mods.update',
  'mcp:mods.remove',
]

const OPERATOR_SET = new Set<MCPScope>(OPERATOR_SCOPES)

/** A preset can only ever select capabilities the client actually asked for.
 *  The API intersects the decision with the parked request anyway, so this is
 *  about not showing a human a selection that is about to be trimmed. */
function presetSelection(
  offered: readonly MCPScope[],
  preset: readonly MCPScope[],
): MCPScope[] {
  return offered.filter((s) => preset.includes(s))
}

// The authorization request id arrives in the query string, put there by the
// API's /authorize redirect. There is no router-level search schema in this app,
// so it is read straight off the location — it is an opaque, single-use,
// unguessable id, and the API is the thing that validates it.
function requestIDFromLocation(): string {
  return new URLSearchParams(window.location.search).get('request') ?? ''
}

/** Send the browser on to wherever the API says. The target is always built
 *  server-side from the *stored* redirect URI of a validated request, never
 *  from anything this page holds — which is what keeps the consent screen from
 *  becoming an open redirect.
 *
 *  An object rather than a bare function so tests can observe the hand-off;
 *  jsdom's `location` is read-only and cannot be stubbed directly. */
export const browserNav = {
  assign: (url: string) => window.location.assign(url),
}

function ConsentSkeleton({ children }: { children: React.ReactNode }) {
  return (
    <div>
      <Header
        title="Connect an agent"
        description="Approve or refuse a remote agent's request to reach your servers"
      />
      <div className="max-w-2xl p-4 sm:p-6">{children}</div>
    </div>
  )
}

function Notice({
  tone,
  children,
}: {
  tone: 'warn' | 'info'
  children: React.ReactNode
}) {
  const cls =
    tone === 'warn'
      ? 'border-amber-500/30 bg-amber-500/10 text-amber-300'
      : 'border-border bg-surface-2/40 text-text-secondary'
  return (
    <div className={`flex items-start gap-2 rounded-md border p-3 text-xs ${cls}`}>
      <ShieldAlert className="mt-0.5 h-4 w-4 flex-shrink-0" />
      <span>{children}</span>
    </div>
  )
}

function ConsentForm({ view }: { view: MCPConsentView }) {
  const { error } = useNotifications()
  const offered = view.scopes.map((s) => s.scope)

  // The initial selection is the read-only subset of what was asked for, never
  // the whole request. A client asking for mutation authority must have a human
  // deliberately add it — the difference between approving a request and
  // noticing one. Nothing here can widen the request: the API intersects
  // whatever comes back with the parked one.
  const [scopes, setScopes] = useState<MCPScope[]>(() =>
    presetSelection(offered, DIAGNOSTIC_SCOPES),
  )
  const [serverIDs, setServerIDs] = useState<string[]>([])
  const [days, setDays] = useState(String(view.default_days))

  // A server can only back the scopes its owner actually holds there, and the
  // API re-checks every (server, scope) pair at approval. Offering a pair it
  // would reject just produces a confusing failure, so eligibility is recomputed
  // as capabilities are ticked.
  const eligible = (s: MCPConsentServer) => scopes.every((sc) => s.scopes.includes(sc))

  // Ticking a capability can strip authority from an already-selected server.
  // Drop those rather than submitting a set the API is bound to refuse.
  useEffect(() => {
    setServerIDs((prev) =>
      prev.filter((id) => {
        const srv = view.servers.find((s) => s.id === id)
        return srv ? eligible(srv) : false
      }),
    )
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scopes])

  const decide = useMutation({
    mutationFn: (approve: boolean) =>
      api.mcp.consent.decide({
        request_id: view.request_id,
        approve,
        scopes,
        server_ids: serverIDs,
        days: Number(days),
      }),
    onSuccess: (res) => browserNav.assign(res.redirect_to),
    onError: (e: Error) => error('Could not complete the connection', e.message),
  })

  const toggleScope = (scope: MCPScope) =>
    setScopes((prev) =>
      prev.includes(scope) ? prev.filter((s) => s !== scope) : [...prev, scope],
    )

  const toggleServer = (id: string) =>
    setServerIDs((prev) =>
      prev.includes(id) ? prev.filter((s) => s !== id) : [...prev, id],
    )

  const selectedOperator = scopes.filter((s) => OPERATOR_SET.has(s))
  // mods.read is the one operator-group scope that changes nothing, so the
  // execute-immediately warning must not fire on it alone.
  const executesImmediately = selectedOperator.some((s) => s !== 'mcp:mods.read')

  const offeredDiagnostics = presetSelection(offered, DIAGNOSTIC_SCOPES)
  const offeredOperator = presetSelection(offered, OPERATOR_SCOPES)

  const dayCount = Number(days)
  const validDays =
    Number.isInteger(dayCount) && dayCount >= 1 && dayCount <= view.max_days
  const ready = scopes.length > 0 && serverIDs.length > 0 && validDays
  const busy = decide.isPending

  return (
    <div className="space-y-4">
      <div className="space-y-4 rounded-lg border border-border bg-surface p-5">
        <div className="flex items-start gap-3">
          <div className="flex h-9 w-9 flex-shrink-0 items-center justify-center rounded-md bg-accent/10">
            <Plug className="h-4 w-4 text-accent" />
          </div>
          <div className="min-w-0">
            <h3 className="font-semibold text-text-primary">
              <span data-testid="consent-client">{view.client_name}</span> wants to
              connect
            </h3>
            <p className="mt-1 text-sm text-text-secondary">
              It will act as you, limited to the servers and capabilities you
              approve below — and to what you can still do yourself.
            </p>
          </div>
        </div>

        {/* A dynamically registered client picked its own name. Saying so is the
            difference between "this is Claude Code" and "this says it is". */}
        {view.client_origin === 'dynamic' && (
          <Notice tone="warn">
            This client registered itself and chose its own name, so treat the
            name as a claim rather than an identity. Approve it only if you just
            started this connection yourself. It will send you back to{' '}
            <code className="font-mono">{view.redirect_host}</code>, which should
            be the tool running on your own machine.
          </Notice>
        )}
        {view.client_origin !== 'dynamic' && (
          <Notice tone="info">
            Registered by an administrator. It will send you back to{' '}
            <code className="font-mono">{view.redirect_host}</code>.
          </Notice>
        )}
      </div>

      <div className="space-y-3 rounded-lg border border-border bg-surface p-5">
        <div>
          <h4 className="font-semibold text-text-primary">Capabilities</h4>
          <p className="mt-1 text-sm text-text-secondary">
            Only read-only capabilities start ticked. Add anything else
            deliberately — you cannot grant more than this agent asked for.
          </p>
        </div>

        {/* Presets set the selection outright rather than adding to it, so
            clicking "Diagnostics only" is a way back to safety and not just
            another way to widen. */}
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={offeredDiagnostics.length === 0}
            onClick={() => setScopes(offeredDiagnostics)}
          >
            Diagnostics only
          </Button>
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={offeredOperator.length === 0}
            onClick={() => setScopes([...offeredDiagnostics, ...offeredOperator])}
          >
            Server operator
          </Button>
          <span className="text-xs text-text-secondary">
            Server operator adds power and mod authority. Backup access is separate,
            is never selected by this preset, and requires your confirmation every time.
          </span>
        </div>

        <div className="space-y-2">
          {view.scopes.map((s) => (
            <label key={s.scope} className="flex items-start gap-2 text-sm">
              <input
                type="checkbox"
                className="mt-1 accent-accent"
                aria-label={s.title}
                checked={scopes.includes(s.scope)}
                onChange={() => toggleScope(s.scope)}
              />
              <span className="min-w-0">
                <span className="flex flex-wrap items-center gap-2">
                  <span className="font-medium text-text-primary">{s.title}</span>
                  {s.sensitive && (
                    <span className="rounded border border-amber-500/30 bg-amber-500/15 px-1.5 py-0.5 text-[10px] text-amber-300">
                      Can change a running server
                    </span>
                  )}
                </span>
                <span className="block text-xs text-text-secondary">
                  {s.description}
                </span>
              </span>
            </label>
          ))}
        </div>
      </div>

      <div className="space-y-3 rounded-lg border border-border bg-surface p-5">
        <div>
          <h4 className="font-semibold text-text-primary">Servers</h4>
          <p className="mt-1 text-sm text-text-secondary">
            The agent can only ever reach the servers you tick here.
          </p>
        </div>
        {view.servers.length === 0 ? (
          <p className="text-sm text-text-secondary">
            You don't have the access this request needs on any server, so there
            is nothing to delegate. Refuse the request.
          </p>
        ) : (
          <div className="grid gap-1 sm:grid-cols-2">
            {view.servers.map((s) => {
              const ok = eligible(s)
              return (
                <label
                  key={s.id}
                  className={`flex items-center gap-2 text-sm ${
                    ok ? 'text-text-primary' : 'text-text-secondary opacity-60'
                  }`}
                  title={
                    ok
                      ? undefined
                      : "You don't hold every ticked capability on this server."
                  }
                >
                  <input
                    type="checkbox"
                    className="accent-accent"
                    aria-label={s.name}
                    checked={serverIDs.includes(s.id)}
                    disabled={!ok}
                    onChange={() => toggleServer(s.id)}
                  />
                  <span className="truncate">{s.name}</span>
                </label>
              )
            })}
          </div>
        )}
        <p className="text-xs text-text-secondary">
          A server you can't back with every ticked capability is greyed out —
          you can't delegate access you don't have.
        </p>
      </div>

      <div className="space-y-3 rounded-lg border border-border bg-surface p-5">
        <div className="space-y-1.5">
          <Label htmlFor="consent-days">Access expires after</Label>
          <div className="flex items-center gap-2">
            <Input
              id="consent-days"
              type="number"
              className="w-28"
              min={1}
              max={view.max_days}
              value={days}
              onChange={(e) => setDays(e.target.value)}
            />
            <span className="text-sm text-text-secondary">days</span>
          </div>
          <p className="text-xs text-text-secondary">
            At most {view.max_days} days. The connection stops working then, and
            you can revoke it sooner from Account → Security at any time.
          </p>
        </div>
      </div>

      {executesImmediately && (
        <Notice tone="warn">
          <strong className="font-semibold">
            These capabilities run immediately.
          </strong>{' '}
          With them ticked, this agent can stop, start, restart, and change the
          installed mods on the servers you select — on its own, at any time
          until this access expires or you revoke it. You will not be asked
          again for each action, and you will find out from the audit trail
          rather than from a prompt. Grant them only to an agent you are
          actively supervising, and prefer "Diagnostics only" otherwise.
        </Notice>
      )}

      {scopes.some((s) => s === 'mcp:actions.request') && (
        <Notice tone="info">
          Requesting a start, stop, or restart only files a request. Nothing runs
          until you approve that specific request with your password. This is
          separate from the direct power capabilities above, which do not ask.
        </Notice>
      )}

      <div className="flex flex-wrap gap-2">
        <Button
          onClick={() => decide.mutate(true)}
          loading={busy && decide.variables === true}
          disabled={!ready || busy}
        >
          Allow access
        </Button>
        <Button
          variant="outline"
          onClick={() => decide.mutate(false)}
          loading={busy && decide.variables === false}
          disabled={busy}
        >
          Refuse
        </Button>
      </div>
    </div>
  )
}

export function ConsentPage() {
  // Captured once: the id must not change under the form while a decision is
  // being made.
  const [requestID] = useState(requestIDFromLocation)

  const { data, isLoading, error } = useQuery({
    queryKey: ['mcp-consent', requestID],
    queryFn: ({ signal }) => api.mcp.consent.get(requestID, signal),
    enabled: requestID !== '',
    // A parked request is single-use and short-lived; refetching it on a window
    // focus would only ever produce a stale or already-decided view.
    retry: false,
    refetchOnWindowFocus: false,
    staleTime: Infinity,
  })

  if (requestID === '') {
    return (
      <ConsentSkeleton>
        <div className="flex items-start gap-2 rounded-md border border-border bg-surface p-4 text-sm text-text-secondary">
          <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-amber-400" />
          <span>
            There's no connection request in this link. Start the connection from
            your agent — for Claude Code, <code>claude mcp add</code> — and follow
            the browser window it opens.
          </span>
        </div>
      </ConsentSkeleton>
    )
  }

  if (isLoading) {
    return (
      <ConsentSkeleton>
        <div className="flex justify-center py-16">
          <div className="h-6 w-6 animate-spin rounded-full border-2 border-accent border-t-transparent" />
        </div>
      </ConsentSkeleton>
    )
  }

  // An expired, unknown, or already-answered request all land here. The API
  // deliberately does not distinguish them, and neither does this.
  if (error || !data || data.already_decided) {
    return (
      <ConsentSkeleton>
        <div className="flex items-start gap-2 rounded-md border border-border bg-surface p-4 text-sm text-text-secondary">
          <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-amber-400" />
          <span>
            This connection request is no longer valid — it may have expired, been
            used already, or been answered in another tab. Requests are
            single-use and short-lived. Run the connect command again to start a
            fresh one.
          </span>
        </div>
      </ConsentSkeleton>
    )
  }

  return (
    <ConsentSkeleton>
      <ConsentForm view={data} />
    </ConsentSkeleton>
  )
}
