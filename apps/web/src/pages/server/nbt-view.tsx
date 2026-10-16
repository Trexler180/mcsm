import { useReducer, useState } from 'react'
import {
  TAG_BYTE,
  TAG_BYTE_ARRAY,
  TAG_COMPOUND,
  TAG_DOUBLE,
  TAG_FLOAT,
  TAG_INT,
  TAG_INT_ARRAY,
  TAG_LIST,
  TAG_LONG,
  TAG_LONG_ARRAY,
  TAG_NAMES,
  TAG_SHORT,
  TAG_STRING,
  type NbtEntry,
  type NbtRoot,
  type NbtTag,
} from '@/lib/nbt'
import { Icon } from '@/ui/icon'
import { cx, plural } from '@/ui/util'

// A .dat file (NBT) as a tree whose values can be edited in place. Edits
// change the parsed tags directly; the parent tracks dirtiness and saves the
// whole tree back in the file's own compression.

const INT_RANGE: Record<number, [number, number]> = {
  [TAG_BYTE]: [-128, 127],
  [TAG_SHORT]: [-32768, 32767],
  [TAG_INT]: [-2147483648, 2147483647],
}

const container = (type: number) => type === TAG_COMPOUND || type === TAG_LIST

function children(tag: NbtTag): NbtEntry[] {
  if (tag.type === TAG_COMPOUND) return tag.value as NbtEntry[]
  if (tag.type === TAG_LIST) return (tag.value as NbtTag[]).map((v, i) => ({ name: `${i}`, tag: v }))
  return []
}

function kind(type: number): string {
  if (type === TAG_STRING) return 'str'
  if (type === TAG_COMPOUND || type === TAG_LIST) return 'box'
  if (type === TAG_FLOAT || type === TAG_DOUBLE) return 'dec'
  if (type === TAG_BYTE_ARRAY || type === TAG_INT_ARRAY || type === TAG_LONG_ARRAY) return 'arr'
  return 'int'
}

/** Parses an edited numeric value for its tag type, or null when it isn't one. */
export function parseScalar(type: number, text: string): number | bigint | null {
  const s = text.trim()
  if (type === TAG_LONG) {
    if (!/^-?\d+$/.test(s)) return null
    const v = BigInt(s)
    return v >= -(2n ** 63n) && v < 2n ** 63n ? v : null
  }
  if (type === TAG_FLOAT || type === TAG_DOUBLE) {
    const v = Number(s)
    return s !== '' && Number.isFinite(v) ? v : null
  }
  if (!/^-?\d+$/.test(s)) return null
  const v = Number(s)
  const [min, max] = INT_RANGE[type] ?? [-Infinity, Infinity]
  return v >= min && v <= max ? v : null
}

function Value({ tag, readOnly, onChange }: { tag: NbtTag; readOnly: boolean; onChange: () => void }) {
  const shown = tag.type === TAG_STRING ? (tag.value as string) : Array.isArray(tag.value) ? (tag.value as Array<number | bigint>).join(', ') : String(tag.value)
  const [draft, setDraft] = useState<string | null>(null)
  const text = draft ?? shown
  const isArray = Array.isArray(tag.value)
  const bad = draft !== null && tag.type !== TAG_STRING && !isArray && parseScalar(tag.type, draft) === null
  const commit = () => {
    if (draft === null) return
    if (tag.type === TAG_STRING) tag.value = draft
    else if (isArray) {
      const parts = draft.split(/[\s,]+/).filter(Boolean)
      const parsed = parts.map((p) => parseScalar(tag.type === TAG_LONG_ARRAY ? TAG_LONG : tag.type === TAG_BYTE_ARRAY ? TAG_BYTE : TAG_INT, p))
      if (parsed.some((p) => p === null)) {
        setDraft(null)
        return
      }
      tag.value = parsed
    } else {
      const v = parseScalar(tag.type, draft)
      if (v === null) {
        setDraft(null)
        return
      }
      tag.value = v
    }
    setDraft(null)
    if (text !== shown) onChange()
  }
  if (readOnly) return <span className={cx('nbt-val', kind(tag.type))}>{shown}</span>
  return (
    <input
      className={cx('nbt-input', kind(tag.type))}
      value={text}
      size={Math.min(60, Math.max(6, text.length + 1))}
      aria-invalid={bad}
      spellCheck={false}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
        if (e.key === 'Escape') setDraft(null)
      }}
    />
  )
}

function Node({ name, tag, depth, readOnly, onChange }: { name: string; tag: NbtTag; depth: number; readOnly: boolean; onChange: () => void }) {
  const [open, setOpen] = useState(depth < 2)
  const box = container(tag.type)
  const kids = box ? children(tag) : []
  return (
    <>
      <div className="nbt-row" style={{ paddingLeft: depth * 16 + 8 }}>
        {box ? (
          <button type="button" className="nbt-toggle" aria-expanded={open} aria-label={`${open ? 'Collapse' : 'Expand'} ${name}`} onClick={() => setOpen((o) => !o)}>
            <Icon name={open ? 'chev-d' : 'chev-r'} size={13} />
          </button>
        ) : (
          <span className="nbt-toggle" />
        )}
        <span className="nbt-name">{name}</span>
        <span className="nbt-type">{TAG_NAMES[tag.type]?.toLowerCase()}</span>
        {box ? <span className="nbt-count">{plural(kids.length, 'entry', 'entries')}</span> : <Value tag={tag} readOnly={readOnly} onChange={onChange} />}
      </div>
      {box && open && kids.map((k, i) => <Node key={`${k.name}-${i}`} name={k.name} tag={k.tag} depth={depth + 1} readOnly={readOnly} onChange={onChange} />)}
    </>
  )
}

export function NbtTree({ root, readOnly, onChange }: { root: NbtRoot; readOnly: boolean; onChange: () => void }) {
  // Edits change tags in place; this re-renders the tree around them.
  const [, rerender] = useReducer((n: number) => n + 1, 0)
  return (
    <div className="nbt" role="tree">
      <Node
        name={root.name || 'root'}
        tag={root.tag}
        depth={0}
        readOnly={readOnly}
        onChange={() => {
          rerender()
          onChange()
        }}
      />
    </div>
  )
}
