import { createRoute } from '@tanstack/react-router'
import { useState } from 'react'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { KeyRound, ExternalLink, Check, Trash2 } from 'lucide-react'
import { Route as rootRoute } from './__root'
import { Header } from '@/components/layout/header'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { api } from '@/lib/api'
import { useNotifications } from '@/store/notifications'
import type { IntegrationMeta } from '@/lib/types'

function IntegrationCard({ meta }: { meta: IntegrationMeta }) {
  const qc = useQueryClient()
  const { success, error } = useNotifications()
  const [value, setValue] = useState('')

  const save = useMutation({
    mutationFn: () => api.settings.integrations.set(meta.key, value.trim()),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['integrations'] })
      success(`${meta.label} saved`, 'Key verified and stored securely')
      setValue('')
    },
    onError: (e: Error) => error('Could not save key', e.message),
  })

  const remove = useMutation({
    mutationFn: () => api.settings.integrations.remove(meta.key),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['integrations'] })
      success(`${meta.label} removed`)
    },
    onError: (e: Error) => error('Could not remove key', e.message),
  })

  return (
    <div className="rounded-lg border border-border bg-surface p-5 space-y-4">
      <div className="flex items-start gap-3">
        <div className="w-9 h-9 rounded-md bg-accent/10 flex items-center justify-center flex-shrink-0">
          <KeyRound className="h-4 w-4 text-accent" />
        </div>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2">
            <h3 className="font-semibold text-text-primary">{meta.label}</h3>
            {meta.configured ? (
              <span className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded border border-green-500/30 bg-green-500/15 text-green-400">
                <Check className="h-3 w-3" />
                Configured ••••{meta.hint}
              </span>
            ) : (
              <span className="text-xs px-2 py-0.5 rounded border border-border bg-surface-2 text-text-secondary">
                Not set
              </span>
            )}
          </div>
          <p className="text-sm text-text-secondary mt-1">{meta.description}</p>
          {meta.doc_url && (
            <a
              href={meta.doc_url}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 text-xs text-accent hover:underline mt-1.5"
            >
              Get a key <ExternalLink className="h-3 w-3" />
            </a>
          )}
        </div>
      </div>

      <div className="flex items-end gap-2">
        <div className="flex-1 space-y-1.5">
          <Label>{meta.configured ? 'Replace key' : 'API key'}</Label>
          <Input
            type="password"
            placeholder="Paste key here"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && value.trim()) save.mutate()
            }}
          />
        </div>
        <Button onClick={() => save.mutate()} loading={save.isPending} disabled={!value.trim()}>
          Save
        </Button>
        {meta.configured && (
          <Button
            variant="outline"
            onClick={() => remove.mutate()}
            loading={remove.isPending}
            title="Remove key"
          >
            <Trash2 className="h-4 w-4 text-red-400" />
          </Button>
        )}
      </div>
    </div>
  )
}

function SettingsPage() {
  const { data: integrations = [], isLoading } = useQuery({
    queryKey: ['integrations'],
    queryFn: api.settings.integrations.list,
  })

  return (
    <div>
      <Header title="Settings" description="Integration API keys, stored encrypted at rest" />
      <div className="p-4 sm:p-6 max-w-2xl space-y-4">
        {isLoading ? (
          <div className="flex justify-center py-16">
            <div className="w-6 h-6 border-2 border-accent border-t-transparent rounded-full animate-spin" />
          </div>
        ) : (
          integrations.map((meta) => <IntegrationCard key={meta.key} meta={meta} />)
        )}
      </div>
    </div>
  )
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  component: SettingsPage,
})
