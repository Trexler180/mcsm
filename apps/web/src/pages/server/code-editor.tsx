import { useEffect, useRef } from 'react'
import { basicSetup } from 'codemirror'
import { EditorState, type Extension } from '@codemirror/state'
import { EditorView, keymap } from '@codemirror/view'
import { HighlightStyle, StreamLanguage, syntaxHighlighting } from '@codemirror/language'
import { tags as t } from '@lezer/highlight'
import { json } from '@codemirror/lang-json'
import { yaml } from '@codemirror/lang-yaml'

// CodeMirror dressed in the console's colours, so it follows the theme the
// way the console does. The document lives in the editor; the parent hears
// about each change and saves with Ctrl+S.

const theme = EditorView.theme({
  '&': { height: '100%', backgroundColor: 'var(--console)', color: 'var(--console-text)', fontSize: '12.5px' },
  '.cm-scroller': { fontFamily: 'var(--mono)', lineHeight: '20px', fontVariantLigatures: 'none' },
  '.cm-content': { padding: '12px 0', caretColor: 'var(--console-text)' },
  '.cm-gutters': { backgroundColor: 'var(--console)', color: 'var(--console-dim)', border: 'none', paddingLeft: '8px' },
  '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--console-text) 5%, transparent)' },
  '.cm-activeLineGutter': { backgroundColor: 'transparent', color: 'var(--console-text)' },
  '&.cm-focused': { outline: 'none' },
  '.cm-cursor, .cm-dropCursor': { borderLeftColor: 'var(--console-text)' },
  '&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection': {
    backgroundColor: 'color-mix(in srgb, var(--focus) 32%, transparent)',
  },
  '.cm-searchMatch': { backgroundColor: 'color-mix(in srgb, var(--c-warn) 30%, transparent)' },
  '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: 'color-mix(in srgb, var(--c-warn) 55%, transparent)' },
  '.cm-matchingBracket': { backgroundColor: 'color-mix(in srgb, var(--console-text) 14%, transparent)', outline: 'none' },
  '.cm-panels': { backgroundColor: 'var(--tile)', color: 'var(--text)', borderColor: 'var(--line)' },
  '.cm-panels input, .cm-panels button': { fontFamily: 'var(--font)', fontSize: '12px' },
  '.cm-tooltip': { backgroundColor: 'var(--tile)', color: 'var(--text)', border: '1px solid var(--line)' },
  '.cm-foldPlaceholder': { backgroundColor: 'transparent', border: 'none', color: 'var(--console-dim)' },
})

const highlight = HighlightStyle.define([
  { tag: [t.comment, t.lineComment, t.blockComment], color: 'var(--console-dim)', fontStyle: 'italic' },
  { tag: [t.string, t.special(t.string)], color: 'var(--c-join)' },
  { tag: [t.number, t.bool, t.null, t.atom], color: 'var(--c-adv)' },
  { tag: [t.propertyName, t.definition(t.propertyName), t.attributeName], color: 'var(--c-cmd)' },
  { tag: [t.keyword, t.operator, t.meta], color: 'var(--c-warn)' },
  { tag: t.invalid, color: 'var(--c-err)' },
])

// server.properties and friends: comments, keys, and everything after the
// first = or : as the value.
const properties = StreamLanguage.define<{ value: boolean }>({
  name: 'properties',
  startState: () => ({ value: false }),
  token(stream, state) {
    if (stream.sol()) {
      state.value = false
      stream.eatSpace()
      if (stream.peek() === '#' || stream.peek() === '!') {
        stream.skipToEnd()
        return 'comment'
      }
    }
    if (!state.value) {
      if (stream.match(/^[^=:]+/)) return 'propertyName'
      if (stream.eat('=') || stream.eat(':')) {
        state.value = true
        return 'operator'
      }
    }
    stream.skipToEnd()
    return /^(true|false)$/.test(stream.current().trim()) ? 'atom' : /^-?\d+(\.\d+)?$/.test(stream.current().trim()) ? 'number' : 'string'
  },
})

export function languageFor(path: string): Extension[] {
  const ext = path.split('.').pop()?.toLowerCase()
  if (ext === 'json' || ext === 'json5' || ext === 'mcmeta') return [json()]
  if (ext === 'yml' || ext === 'yaml') return [yaml()]
  if (ext === 'properties' || ext === 'cfg' || ext === 'conf' || ext === 'ini' || ext === 'lang') return [properties]
  return []
}

export function CodeEditor({
  path,
  doc,
  readOnly,
  onChange,
  onSave,
}: {
  /** Picks the language. */
  path: string
  /** The starting text. Later changes are ignored, so saving doesn't reset
   *  the cursor or undo history; give the editor a new key to start over. */
  doc: string
  readOnly?: boolean
  onChange: (text: string) => void
  onSave: () => void
}) {
  const host = useRef<HTMLDivElement>(null)
  const view = useRef<EditorView | null>(null)
  // The latest callbacks, read from inside CodeMirror's listeners.
  const handlers = useRef({ onChange, onSave })
  handlers.current = { onChange, onSave }
  const initial = useRef(doc)

  useEffect(() => {
    if (!host.current) return
    const v = new EditorView({
      parent: host.current,
      state: EditorState.create({
        doc: initial.current,
        extensions: [
          keymap.of([
            {
              key: 'Mod-s',
              preventDefault: true,
              run: () => {
                handlers.current.onSave()
                return true
              },
            },
          ]),
          basicSetup,
          theme,
          syntaxHighlighting(highlight),
          EditorState.readOnly.of(!!readOnly),
          EditorView.contentAttributes.of({ 'aria-label': `Contents of ${path.split('/').pop()}` }),
          EditorView.updateListener.of((u) => {
            if (u.docChanged) handlers.current.onChange(u.state.doc.toString())
          }),
          ...languageFor(path),
        ],
      }),
    })
    view.current = v
    return () => {
      v.destroy()
      view.current = null
    }
  }, [path, readOnly])

  return <div ref={host} className="cm-host" />
}
