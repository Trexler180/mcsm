import { useEffect, useState } from 'react'
import { useQueryClient, type QueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { FixPlan, FixStep, MyServerPermissions, Server, ServerPermission, StartupProblem } from '@/lib/types'
import { useAuthStore } from '@/store/auth'
import { useGo } from '@/shell/nav'
import { paths } from '@/shell/paths'
import { Btn, Choices, TextBtn } from '@/ui/controls'
import { act } from '@/ui/failure'
import { Icon, type IconName } from '@/ui/icon'
import { Dialog } from '@/ui/overlays'
import { permissionLabel } from '@/lib/permissions'
import { toast } from '@/ui/toasts'
import { allowed } from './data'

// Why a server didn't start, and how to fix it. Every kind of problem the
// panel recognises reads the same: one or more ways to fix it, each a short
// list of steps spelled out before anything changes, the recommended one
// picked to begin with. Applying one runs its steps and then starts the
// server, as one operation.

/** Whether this person may apply a plan, and if not, why. */
export function planAllowed(plan: FixPlan, perms: MyServerPermissions | undefined, admin: boolean): { ok: boolean; why?: string } {
  if (plan.requires_admin && !admin) return { ok: false, why: 'Only an administrator can apply this fix.' }
  const missing = plan.permissions.filter((p) => !allowed(perms, p as ServerPermission))
  if (missing.length) return { ok: false, why: `You need the ${missing.map((p) => `"${permissionLabel(p as ServerPermission)}"`).join(' and ')} permission.` }
  if (plan.unresolved?.length) return { ok: false, why: `The panel couldn’t find ${plan.unresolved.join(', ')} for this server.` }
  return { ok: true }
}

export function recommendedPlan(problem: StartupProblem): FixPlan | undefined {
  return problem.plans.find((p) => p.recommended) ?? problem.plans[0]
}

export async function applyPlan(server: Server, plan: FixPlan, qc: QueryClient) {
  const r = await act(plan.title.charAt(0).toLowerCase() + plan.title.slice(1), () => api.servers.applyFix(server.id, plan.id))
  if (r === undefined) return
  toast('info', `Fixing ${server.name}`, 'It starts once the fix is in.')
  void qc.invalidateQueries({ queryKey: ['startup-problem', server.id] })
  void qc.invalidateQueries({ queryKey: ['fleet'] })
}

/** The fix most people want, as one button. */
export function quickFix(problem: StartupProblem, perms: MyServerPermissions | undefined, admin: boolean) {
  const plan = recommendedPlan(problem)
  if (!plan) return null
  const can = planAllowed(plan, perms, admin)
  return { plan, label: plan.title, allowed: can.ok, why: can.why }
}

const STEP_ICON: Record<FixStep['kind'], IconName> = {
  disable_mods: 'minus',
  install_mod: 'download',
  set_java: 'settings',
  start: 'play',
}

export function FixSheet({ open, onClose, server, problem, perms }: { open: boolean; onClose: () => void; server: Server; problem: StartupProblem; perms: MyServerPermissions | undefined }) {
  const qc = useQueryClient()
  const go = useGo()
  const admin = useAuthStore((s) => s.user?.role === 'admin')
  const [pick, setPick] = useState<string | null>(null)
  useEffect(() => {
    if (open) setPick(null)
  }, [open, problem.problem?.detected_at])
  const p = problem.problem
  if (!p) return null
  const rec = recommendedPlan(problem)
  const plan = problem.plans.find((x) => x.id === pick) ?? rec
  const can = plan ? planAllowed(plan, perms, admin) : { ok: false }
  return (
    <Dialog
      open={open}
      onClose={onClose}
      size="wide"
      title={p.headline}
      foot={
        <>
          <TextBtn
            quiet
            className="left"
            onClick={() => {
              onClose()
              go(paths.server(server.id) + '?view=files')
            }}
          >
            Read the full log
          </TextBtn>
          {plan && (
            <Btn
              variant="primary"
              disabled={!can.ok}
              tip={can.why}
              onClick={() => {
                onClose()
                void applyPlan(server, plan, qc)
              }}
            >
              {plan.title}
            </Btn>
          )}
        </>
      }
    >
      <div className="stack" style={{ gap: 18 }}>
        {p.detail && <p>{p.detail}</p>}
        {p.mods.length > 0 && (
          <div className="involved">
            {p.mods.map((m, i) => (
              <span key={m} className="row" style={{ gap: 8 }}>
                {i > 0 && <span className="involved-sep t3 small">{p.kind === 'incompatible' ? <Icon name="x" size={13} /> : 'and'}</span>}
                <span className="mod-chip">
                  <span className="strong">{m}</span>
                </span>
              </span>
            ))}
            {p.kind === 'java_version' && p.required_java ? (
              <span className="mod-chip missing">
                <span className="java-badge">{p.required_java}</span>
                <span>
                  <span className="strong">Needs Java {p.required_java}</span>
                  <span className="t3 small" style={{ display: 'block' }}>
                    or newer
                  </span>
                </span>
              </span>
            ) : null}
          </div>
        )}
        {!p.live && <p className="small warn">The node couldn’t be asked just now, so the fixes below may be out of date.</p>}
        {problem.plans.length > 1 && (
          <div>
            <div className="field-label" style={{ marginBottom: 8 }}>
              Ways to fix it
            </div>
            <Choices
              value={plan?.id ?? ''}
              onChange={setPick}
              options={problem.plans.map((x) => ({
                value: x.id,
                label: (
                  <>
                    {x.title}
                    {x.recommended && (
                      <span className="t3" style={{ fontWeight: 400 }}>
                        {' '}
                        (recommended)
                      </span>
                    )}
                  </>
                ),
                desc: x.description,
              }))}
            />
          </div>
        )}
        {plan ? (
          <div>
            <div className="field-label" style={{ marginBottom: 4 }}>
              {problem.plans.length > 1 ? 'What this does' : 'The fix'}
            </div>
            <div className="fix-steps">
              {plan.steps.map((st, i) => (
                <div key={i} className="fix-step">
                  <span className="java-badge sm">
                    <Icon name={STEP_ICON[st.kind] ?? 'circle'} size={13} />
                  </span>
                  <div className="grow">
                    <div className="strong">{st.label}</div>
                  </div>
                </div>
              ))}
            </div>
            <p className="small t3" style={{ marginTop: 8 }}>
              {problem.plans.length === 1 ? plan.description + ' ' : ''}Your worlds aren’t touched.
            </p>
            {!can.ok && can.why && <p className="small warn" style={{ marginTop: 6 }}>{can.why}</p>}
          </div>
        ) : (
          <p className="t2">The panel doesn’t know a fix for this one. The log below shows what the server printed.</p>
        )}
        {p.log.length > 0 && (
          <details className="raw-log">
            <summary className="small t2">What {server.name} printed</summary>
            <div className="codeblock" style={{ paddingRight: 13, marginTop: 8 }}>
              {p.log.join('\n')}
            </div>
          </details>
        )}
      </div>
    </Dialog>
  )
}
