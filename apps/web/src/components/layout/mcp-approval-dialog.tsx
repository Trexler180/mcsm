import { useState } from 'react'
import { create } from 'zustand'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { ShieldAlert } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Dialog } from '@/components/ui/dialog'
import {
  StepUpFields,
  stepUpReady,
  useMfaEnabled,
} from '@/components/settings/step-up-fields'
import { api } from '@/lib/api'
import { actionPhrase, type MCPActionAlert } from '@/lib/mcp-action-alert'
import { useNotifications } from '@/store/notifications'

// The step-up half of the global approval prompt.
//
// A toast cannot hold a password field — it is small, it is transient, and a
// credential typed into something that might slide away is a credential typed
// somewhere it should not be. So when policy requires a step-up, the toast's
// button opens this instead, and the dialog is mounted at the root next to the
// Toaster so it is reachable from whatever screen the operator happened to be
// on when the agent asked.

interface ApprovalDialogState {
  alert: MCPActionAlert | null
  open: (alert: MCPActionAlert) => void
  close: () => void
}

/** Module-level, like the toast store, so anything can raise the dialog without
 *  a provider or a prop chain from the root. */
export const useMcpApprovalDialog = create<ApprovalDialogState>()((set) => ({
  alert: null,
  open: (alert) => set({ alert }),
  close: () => set({ alert: null }),
}))

export function McpApprovalDialog() {
  const { alert, close } = useMcpApprovalDialog()
  const qc = useQueryClient()
  const { success, error } = useNotifications()
  const mfaEnabled = useMfaEnabled()
  const [password, setPassword] = useState('')
  const [totp, setTotp] = useState('')

  const reset = () => {
    setPassword('')
    setTotp('')
    close()
  }

  const approve = useMutation({
    mutationFn: () => {
      if (!alert) throw new Error('no request')
      return api.mcp.actionRequests.approve(alert.requestId, {
        password,
        totp_code: totp.trim() || undefined,
      })
    },
    onSuccess: (settled) => {
      const label = alert ? actionPhrase(alert.action) : 'The action'
      reset()
      qc.invalidateQueries({ queryKey: ['mcp-action-requests'] })
      // An executed action changes what the rest of the panel shows.
      qc.invalidateQueries({ queryKey: ['servers'] })
      if (settled.status === 'failed') {
        error('The action failed', settled.failure_reason ?? 'The server did not accept it.')
        return
      }
      success(`Approved: ${label}`, 'The action ran once.')
    },
    // The dialog stays open on failure so a mistyped password can be corrected
    // without going back to find the request again.
    onError: (e: Error) => error('Could not approve', e.message),
  })

  if (!alert) return null
  const ready = stepUpReady(password, totp, mfaEnabled)

  return (
    <Dialog
      open
      onClose={reset}
      title={`Approve: ${actionPhrase(alert.action)}`}
      description={`${alert.clientName} asked to ${actionPhrase(alert.action)}${
        alert.serverName ? ` on ${alert.serverName}` : ''
      }.`}
    >
      <div className="space-y-4">
        {/* The agent wrote this. It is shown so a human can judge whether the
            stated reason matches what they can see for themselves — never as an
            instruction, and never interpreted by the panel. */}
        {alert.reason && (
          <div className="rounded-md border border-border bg-surface-2/40 p-3">
            <div className="mb-1 flex items-center gap-1.5 text-[10px] uppercase tracking-wide text-text-secondary">
              <ShieldAlert className="h-3 w-3 text-amber-400" />
              Agent's stated reason — untrusted text
            </div>
            <p className="whitespace-pre-wrap break-words text-xs text-text-secondary">
              {alert.reason}
            </p>
          </div>
        )}

        {alert.action.startsWith('upgrade:') && (
          <p className="text-xs text-amber-300">
            Approval creates one full restore-point backup, changes the server
            version and mods, watches the boot, and automatically restores the
            backup if startup is unhealthy.
          </p>
        )}

        <p className="text-xs text-text-secondary">
          Confirm it's you. This runs the action once, right now, and re-checks
          that the connection still has the access it needs.
        </p>
        <StepUpFields
          idPrefix={`mcp-approve-${alert.requestId}`}
          password={password}
          totp={totp}
          onPassword={setPassword}
          onTotp={setTotp}
          mfaEnabled={mfaEnabled}
          onSubmit={() => ready && approve.mutate()}
        />

        <div className="flex justify-end gap-3">
          <Button variant="outline" onClick={reset} disabled={approve.isPending}>
            Cancel
          </Button>
          <Button
            onClick={() => approve.mutate()}
            loading={approve.isPending}
            disabled={!ready}
          >
            Approve and run
          </Button>
        </div>
      </div>
    </Dialog>
  )
}
