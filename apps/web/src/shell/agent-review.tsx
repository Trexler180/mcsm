import { useEffect, useState } from 'react'
import { create } from 'zustand'
import { useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import { actionPhrase, type MCPActionAlert } from '@/lib/mcp-action-alert'
import { Btn } from '@/ui/controls'
import { failureOf } from '@/ui/failure'
import { Dialog } from '@/ui/overlays'
import { checkStepUp, StepUpFields, useHasMfa, type StepUpErrors } from '@/ui/stepup'
import type { StepUpValue } from '@/ui/store'
import { toast } from '@/ui/toasts'

// Approving an agent's request when your rules ask for your password. A toast
// can't hold a password field (it's small and it slides away), so its button
// opens this, mounted with the shell so it works from any page.

interface ApprovalDialogState {
  alert: MCPActionAlert | null
  open: (alert: MCPActionAlert) => void
  close: () => void
}

/** Module-level, like the toasts, so anything can raise it. */
export const useMcpApprovalDialog = create<ApprovalDialogState>()((set) => ({
  alert: null,
  open: (alert) => set({ alert }),
  close: () => set({ alert: null }),
}))

export function McpApprovalDialog() {
  const { alert, close } = useMcpApprovalDialog()
  const qc = useQueryClient()
  const mfa = useHasMfa()
  const [v, setV] = useState<StepUpValue>({ password: '', code: '' })
  const [errors, setErrors] = useState<StepUpErrors | null>(null)
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    setV({ password: '', code: '' })
    setErrors(null)
  }, [alert?.requestId])
  if (!alert) return null

  const ready = checkStepUp(v, mfa) === null
  const what = actionPhrase(alert.action)
  const approve = async () => {
    const e = checkStepUp(v, mfa)
    setErrors(e)
    if (e || busy) return
    setBusy(true)
    try {
      const settled = await api.mcp.actionRequests.approve(alert.requestId, { password: v.password, totp_code: v.code.trim() || undefined })
      close()
      void qc.invalidateQueries({ queryKey: ['mcp-action-requests'] })
      void qc.invalidateQueries({ queryKey: ['fleet'] })
      if (settled.status === 'failed') toast('bad', 'It didn’t work', settled.failure_reason ?? 'The server didn’t accept it.')
      else toast('ok', `Approved: ${what}`, 'It ran once.')
    } catch (err) {
      // Stays open, so a mistyped password can be fixed without finding the request again.
      toast('bad', 'Couldn’t approve it', failureOf(err).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      open
      onClose={close}
      title={`Approve: ${what}`}
      foot={
        <>
          <Btn variant="quiet" disabled={busy} onClick={close}>
            Cancel
          </Btn>
          <Btn variant="primary" disabled={!ready} loading={busy} onClick={() => void approve()}>
            Approve and run
          </Btn>
        </>
      }
    >
      <div className="stack" style={{ gap: 14 }}>
        <p className="t2">
          {alert.clientName} asked to {what}
          {alert.serverName ? ` on ${alert.serverName}` : ''}.
        </p>
        {/* The agent wrote this: shown so a person can weigh it against what
            they can see, never acted on. */}
        {alert.reason && (
          <div className="untrusted small" style={{ marginTop: 0 }}>
            <div className="t3 tiny" style={{ marginBottom: 2 }}>
              What the agent says (untrusted text)
            </div>
            {alert.reason}
          </div>
        )}
        {alert.action.startsWith('upgrade:') && (
          <p className="small t2">Approving creates one full restore-point backup, changes the server version and mods, watches it start, and restores the backup on its own if it doesn’t start cleanly.</p>
        )}
        <p className="small t2">Confirm it’s you. This runs once, straight away, after checking the connection still has the access it needs.</p>
        <StepUpFields
          value={v}
          mfa={mfa}
          errors={errors}
          onChange={(x) => {
            setV(x)
            setErrors(null)
          }}
          onEnter={() => void approve()}
        />
      </div>
    </Dialog>
  )
}
