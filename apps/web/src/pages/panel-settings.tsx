import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '@/lib/api'
import type { IntegrationMeta } from '@/lib/types'
import { Btn, TextBtn } from '@/ui/controls'
import { act } from '@/ui/failure'
import { PageHead } from '@/ui/page'
import { Loaded, SkForm } from '@/ui/skeleton'
import { ask } from '@/ui/store'
import { toast } from '@/ui/toasts'

// Panel-wide settings, for administrators: the keys MCSM uses to reach mod
// sites, and which versions are running. Access keys and AI agents belong to
// each person, so they're under Account.

export function PanelSettingsPage() {
  const integrations = useQuery({ queryKey: ['integrations'], queryFn: () => api.settings.integrations.list() })
  return (
    <div className="scroll">
      <div className="page narrow">
        <PageHead title="Settings" lede="For the whole panel. Your own access keys and AI agents are under your account." />
        <Loaded query={integrations} sk={<SkForm sections={2} />} what="the integrations">
          {(list) => (
            <div className="stack">
              {list.map((it) => (
                <Integration key={it.key} it={it} />
              ))}
            </div>
          )}
        </Loaded>
        <About />
      </div>
    </div>
  )
}

function Integration({ it }: { it: IntegrationMeta }) {
  const qc = useQueryClient()
  const [val, setVal] = useState('')
  const [busy, setBusy] = useState(false)
  const refresh = () => void qc.invalidateQueries({ queryKey: ['integrations'] })
  const save = async () => {
    setBusy(true)
    const r = await act(`save the ${it.label} key`, () => api.settings.integrations.set(it.key, val.trim()).then(() => true))
    setBusy(false)
    if (!r) return
    toast('ok', `Saved your ${it.label} key`, 'It was checked and is stored encrypted.')
    setVal('')
    refresh()
    void qc.invalidateQueries({ queryKey: ['mod-sources'] })
  }
  const remove = async () => {
    if (!(await ask({ title: `Remove the ${it.label} key?`, body: 'Anything that needs it stops working until a key is added again. Installed mods keep working.', confirm: 'Remove', danger: true }))) return
    const r = await act(`remove the ${it.label} key`, () => api.settings.integrations.remove(it.key).then(() => true))
    if (!r) return
    toast('ok', `Removed the ${it.label} key`)
    refresh()
  }
  return (
    <div className="form-section">
      <h2>{it.label}</h2>
      <p className="lede">{it.description}</p>
      {it.configured && (
        <p className="small t2" style={{ marginBottom: 12 }}>
          Set, with a key ending in <code>{it.hint}</code>.{' '}
          <TextBtn quiet onClick={() => void remove()}>
            Remove it
          </TextBtn>
        </p>
      )}
      <form
        className="row"
        style={{ maxWidth: 520, gap: 8 }}
        onSubmit={(e) => {
          e.preventDefault()
          if (val.trim()) void save()
        }}
      >
        <input className="input mono grow" type="password" value={val} onChange={(e) => setVal(e.target.value)} placeholder={it.configured ? 'Paste a new key to replace it' : 'Paste the API key'} aria-label={`${it.label} API key`} autoComplete="off" />
        <Btn type="submit" disabled={!val.trim()} loading={busy}>
          Save
        </Btn>
      </form>
      {it.doc_url && (
        <p className="small" style={{ marginTop: 10 }}>
          <a className="link quiet" href={it.doc_url} target="_blank" rel="noreferrer">
            Where to get a key
          </a>
        </p>
      )}
    </div>
  )
}

function About() {
  const health = useQuery({ queryKey: ['health'], queryFn: () => api.health(), staleTime: 60 * 60_000 })
  return (
    <div className="form-section" style={{ borderBottom: 0 }}>
      <h2>About</h2>
      <dl className="facts">
        <dt>Web</dt>
        <dd className="mono">{__APP_VERSION__}</dd>
        <dt>API</dt>
        <dd className="mono">{health.data?.version ?? '…'}</dd>
      </dl>
    </div>
  )
}
