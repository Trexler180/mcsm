import { useEffect, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Dialog } from '@/components/ui/dialog'
import { StepUpFields, stepUpReady, useMfaEnabled } from '@/components/settings/step-up-fields'
import { api } from '@/lib/api'
import { useRebootTracker } from '@/lib/reboot-tracker'
import { useNotifications } from '@/store/notifications'
import type { Node } from '@/lib/types'

// Rebooting a host takes every server on it down and, when the dashboard runs
// on that same machine, the dashboard too. The dialog says so in plain words
// and asks for the password (+ TOTP) the API will demand anyway, so the
// decision is made with the consequences on screen rather than discovered.

interface RebootDialogProps {
  node: Node | null
  serverCount: number
  onClose: () => void
}

export function RebootNodeDialog({ node, serverCount, onClose }: RebootDialogProps) {
  const qc = useQueryClient()
  const { error } = useNotifications()
  const startTracking = useRebootTracker((s) => s.start)
  const mfaEnabled = useMfaEnabled()
  const [password, setPassword] = useState('')
  const [totp, setTotp] = useState('')

  // A fresh dialog never carries a password typed for another node.
  useEffect(() => {
    setPassword('')
    setTotp('')
  }, [node?.id])

  const mutation = useMutation({
    mutationFn: () =>
      api.nodes.reboot(node!.id, {
        password,
        totp_code: mfaEnabled ? totp.trim() : undefined,
      }),
    onSuccess: (res) => {
      // Hand over to the full-screen rebooting view, which follows the host
      // down and back up and reconnects on its own.
      if (node) startTracking(node, res.requested_at)
      qc.invalidateQueries({ queryKey: ['nodes'] })
      onClose()
    },
    onError: (e: Error) => error('Reboot refused', e.message),
  })

  const ready = stepUpReady(password, totp, mfaEnabled)
  const submit = () => {
    if (ready && !mutation.isPending) mutation.mutate()
  }

  return (
    <Dialog
      open={node !== null}
      onClose={() => !mutation.isPending && onClose()}
      title={`Reboot ${node?.name ?? 'node'}`}
      className="max-w-md"
    >
      <div className="space-y-4">
        <div className="flex gap-3 rounded-md border border-red-900/60 bg-red-950/30 p-3 text-sm">
          <AlertTriangle className="mt-0.5 h-4 w-4 flex-shrink-0 text-red-400" />
          <div className="space-y-1.5 text-text-secondary">
            <p className="text-text-primary">This restarts the whole machine.</p>
            <ul className="list-disc space-y-1 pl-4">
              <li>
                {serverCount > 0
                  ? `All ${serverCount} server${serverCount !== 1 ? 's' : ''} on this node will be stopped gracefully first; players are disconnected.`
                  : 'Any server running on this node is stopped gracefully first.'}
              </li>
              <li>
                If this dashboard runs on the same machine, it will be unreachable until the host is
                back up.
              </li>
              <li>
                Servers set to auto-start come back when the dashboard itself restarts, which happens
                when it runs on this machine. Otherwise start them again once the node is online.
              </li>
            </ul>
          </div>
        </div>

        <StepUpFields
          idPrefix="reboot"
          password={password}
          totp={totp}
          onPassword={setPassword}
          onTotp={setTotp}
          mfaEnabled={mfaEnabled}
          onSubmit={submit}
        />

        <div className="flex justify-end gap-3 pt-1">
          <Button variant="outline" onClick={onClose} disabled={mutation.isPending}>
            Cancel
          </Button>
          <Button
            variant="destructive"
            onClick={submit}
            disabled={!ready}
            loading={mutation.isPending}
          >
            Reboot host
          </Button>
        </div>
      </div>
    </Dialog>
  )
}
