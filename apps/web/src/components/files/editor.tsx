import { useEffect, useRef, useState } from 'react'
import { EditorView, basicSetup } from 'codemirror'
import { EditorState } from '@codemirror/state'
import { oneDark } from '@codemirror/theme-one-dark'
import { json } from '@codemirror/lang-json'
import { yaml } from '@codemirror/lang-yaml'
import { Save, Loader2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useNotifications } from '@/store/notifications'
import { api } from '@/lib/api'

interface FileEditorProps {
  serverId: string
  path: string
}

function getExtensions(filename: string) {
  const ext = filename.split('.').pop()?.toLowerCase()
  switch (ext) {
    case 'json':
      return [json()]
    case 'yml':
    case 'yaml':
      return [yaml()]
    default:
      return []
  }
}

export function FileEditor({ serverId, path }: FileEditorProps) {
  const editorRef = useRef<HTMLDivElement>(null)
  const viewRef = useRef<EditorView | null>(null)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const { success, error } = useNotifications()

  useEffect(() => {
    if (!editorRef.current || !path) return

    setLoading(true)
    api.files
      .readContent(serverId, path)
      .then((content) => {
        const filename = path.split('/').pop() ?? ''
        const state = EditorState.create({
          doc: content,
          extensions: [
            basicSetup,
            oneDark,
            ...getExtensions(filename),
            EditorView.theme({
              '&': { height: '100%', backgroundColor: '#0f0f0f' },
              '.cm-scroller': { overflow: 'auto', fontFamily: 'JetBrains Mono, Consolas, monospace' },
            }),
          ],
        })

        if (viewRef.current) viewRef.current.destroy()
        const view = new EditorView({ state, parent: editorRef.current! })
        viewRef.current = view
      })
      .catch((e: Error) => error('Failed to load file', e.message))
      .finally(() => setLoading(false))

    return () => {
      viewRef.current?.destroy()
      viewRef.current = null
    }
  }, [serverId, path])

  const save = async () => {
    if (!viewRef.current) return
    const content = viewRef.current.state.doc.toString()
    setSaving(true)
    try {
      await api.files.writeContent(serverId, path, content)
      success('Saved')
    } catch (e) {
      error('Save failed', e instanceof Error ? e.message : 'Unknown error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex flex-col h-full">
      <div className="flex items-center justify-between gap-2 px-4 py-2 border-b border-border bg-surface">
        <span className="min-w-0 truncate text-sm text-text-secondary font-mono">{path}</span>
        <Button size="sm" onClick={save} loading={saving} className="flex-shrink-0">
          <Save className="h-3.5 w-3.5" />
          Save
        </Button>
      </div>
      <div className="flex-1 relative overflow-hidden">
        {loading && (
          <div className="absolute inset-0 flex items-center justify-center bg-[#0f0f0f]">
            <Loader2 className="h-5 w-5 text-accent animate-spin" />
          </div>
        )}
        <div ref={editorRef} className="h-full" />
      </div>
    </div>
  )
}
