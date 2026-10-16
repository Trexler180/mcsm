import { useState, type CSSProperties } from 'react'
import { BLOCKS, BLOCK_KINDS, type BlockKind } from './blocks'
import { hashStr, pick, rng } from './util'

// Pixel art: a server's icon (or the block standing in for one), player
// heads, avatars, and Minecraft's formatted text.

function shadeHex(c: string, k: number): string {
  const n = parseInt(c.slice(1), 16)
  const f = (v: number) => Math.max(0, Math.min(255, Math.round(v * k)))
  return '#' + ((f(n >> 16) << 16) | (f((n >> 8) & 255) << 8) | f(n & 255)).toString(16).padStart(6, '0')
}

const blockCache = new Map<string, string[]>()

function blockPixels(kind: BlockKind, seed: string): string[] {
  const key = kind + ':' + seed
  const hit = blockCache.get(key)
  if (hit) return hit
  const def = BLOCKS[kind] ?? BLOCKS.stone
  const r = rng('block:' + key)
  const px: string[] = []
  for (let y = 0; y < 8; y++) {
    for (let x = 0; x < 8; x++) {
      const pal = def.pal as Record<string, string>
      px.push(shadeHex(pal[def.rows[y][x]], 0.95 + r() * 0.1))
    }
  }
  blockCache.set(key, px)
  return px
}

/** The block a server shows until it has its own icon, chosen from its id so
 *  it never changes. */
export function blockFor(id: string): BlockKind {
  return BLOCK_KINDS[hashStr(id) % BLOCK_KINDS.length]
}

function Pixels({ px, size, className, style, label }: { px: string[]; size: number; className?: string; style?: CSSProperties; label?: string }) {
  return (
    <svg
      className={className}
      viewBox="0 0 8 8"
      width={size}
      height={size}
      style={{ shapeRendering: 'crispEdges', display: 'block', ...style }}
      aria-hidden={label ? undefined : true}
      role={label ? 'img' : undefined}
      aria-label={label}
    >
      {px.map((c, i) => (
        <rect key={i} x={i % 8} y={Math.floor(i / 8)} width="1.02" height="1.02" fill={c} />
      ))}
    </svg>
  )
}

/** A server's icon: its server-icon.png when there is one, otherwise its block. */
export function ServerIcon({ id, icon, size = 40, className, style }: { id: string; icon?: string | null; size?: number; className?: string; style?: CSSProperties }) {
  if (icon) {
    return <img src={icon} alt="" width={size} height={size} className={className} style={{ imageRendering: 'pixelated', display: 'block', ...style }} />
  }
  return <Pixels px={blockPixels(blockFor(id), id)} size={size} className={className} style={style} />
}

export function BlockSwatch({ kind, size = 28 }: { kind: BlockKind; size?: number }) {
  return <Pixels px={blockPixels(kind, 'swatch')} size={size} style={{ borderRadius: 4 }} />
}

// ---- player heads -------------------------------------------------------------------

const SKIN = ['#f0c6a0', '#e0ac85', '#c68a5f', '#a86f4a', '#8a5636', '#6b3f26', '#f6d7bd']
const HAIR = ['#2b1d12', '#4a2f1b', '#6e4423', '#a0682f', '#d9b14f', '#e8d48a', '#1c1c1c', '#7a2e1d', '#c24a2a', '#ececec', '#3d4a8a', '#2f6d3a']
const EYES = ['#3b6fd1', '#2f8a5a', '#5b3a1f', '#2d2d2d', '#6b47b8', '#2aa0a8']
const headCache = new Map<string, string[]>()

/** A face drawn from the name, shown while the real skin loads or when there
 *  isn't one (Bedrock players, offline lookups). */
function headPixels(name: string): string[] {
  const hit = headCache.get(name)
  if (hit) return hit
  const r = rng('head:' + name)
  const skin = pick(r, SKIN)
  const hair = pick(r, HAIR)
  const eye = pick(r, EYES)
  const style = Math.floor(r() * 4) // 0 short, 1 fringe, 2 long, 3 hood
  const beard = r() < 0.18
  const hood = style === 3 ? pick(r, ['#3a6ea5', '#a53a3a', '#3f8f4f', '#7a5aa8', '#d08a2a', '#404040']) : null
  const top = hood || hair
  const px: string[] = []
  for (let y = 0; y < 8; y++) {
    for (let x = 0; x < 8; x++) {
      const side = x === 0 || x === 7
      let c = skin
      if (y <= 1) c = top
      else if (y === 2 && (side || style === 1 || (style !== 0 && r() < 0.5))) c = top
      else if (side && (style === 2 || style === 3) && y <= 5) c = top
      else if (y === 4 && (x === 1 || x === 6)) c = '#ffffff'
      else if (y === 4 && (x === 2 || x === 5)) c = eye
      else if (y === 5 && (x === 3 || x === 4)) c = shadeHex(skin, 0.86)
      else if (y === 6 && x >= 2 && x <= 5) c = beard ? hair : shadeHex(skin, 0.7)
      else if (beard && y === 7 && x >= 1 && x <= 6) c = hair
      px.push(c === skin || c === top ? shadeHex(c, 0.94 + r() * 0.12) : c)
    }
  }
  headCache.set(name, px)
  return px
}

/** A player's face from their skin, falling back to a drawn one. Bedrock
 *  names (a leading dot) have no Java skin to look up. */
export function PlayerHead({ name, size = 20, skin = true }: { name: string; size?: number; skin?: boolean }) {
  const [failed, setFailed] = useState(false)
  const java = skin && !failed && !!name && !name.startsWith('.')
  if (java) {
    return (
      <img
        className="phead"
        src={`https://mc-heads.net/avatar/${encodeURIComponent(name)}/${Math.max(16, size * 2)}`}
        alt=""
        width={size}
        height={size}
        loading="lazy"
        style={{ imageRendering: 'pixelated' }}
        onError={() => setFailed(true)}
      />
    )
  }
  return <Pixels px={headPixels(name || '?')} size={size} className="phead" />
}

export function Avatar({ name, size = 26 }: { name: string | null | undefined; size?: number }) {
  const n = name || '?'
  const hue = hashStr(n) % 360
  return (
    <span className="avatar" aria-hidden="true" style={{ width: size, height: size, fontSize: size * 0.42, background: `hsl(${hue} 22% 40%)` }}>
      {n.charAt(0).toUpperCase()}
    </span>
  )
}

// ---- MOTD ------------------------------------------------------------------------------
// Minecraft formatting codes, rendered the way the multiplayer screen does.

export const MC_COLORS: Record<string, string> = {
  0: '#000000', 1: '#0000AA', 2: '#00AA00', 3: '#00AAAA', 4: '#AA0000', 5: '#AA00AA', 6: '#FFAA00', 7: '#AAAAAA',
  8: '#555555', 9: '#5555FF', a: '#55FF55', b: '#55FFFF', c: '#FF5555', d: '#FF55FF', e: '#FFFF55', f: '#FFFFFF',
}
export const MC_COLOR_NAMES: Record<string, string> = {
  0: 'Black', 1: 'Dark blue', 2: 'Dark green', 3: 'Dark aqua', 4: 'Dark red', 5: 'Dark purple', 6: 'Gold', 7: 'Gray',
  8: 'Dark gray', 9: 'Blue', a: 'Green', b: 'Aqua', c: 'Red', d: 'Light purple', e: 'Yellow', f: 'White',
}

export interface MotdRun {
  text: string
  color: string | null
  b: boolean
  i: boolean
  u: boolean
  s: boolean
  k: boolean
}

const PLAIN = { color: null, b: false, i: false, u: false, s: false, k: false }

export function parseMotd(text: string): MotdRun[] {
  const out: MotdRun[] = []
  let st: Omit<MotdRun, 'text'> = { ...PLAIN }
  let buf = ''
  const flush = () => {
    if (buf) {
      out.push({ text: buf, ...st })
      buf = ''
    }
  }
  for (let i = 0; i < text.length; i++) {
    const ch = text[i]
    if ((ch === '§' || ch === '&') && i + 1 < text.length) {
      const code = text[i + 1].toLowerCase()
      if (MC_COLORS[code] || 'klmnor'.includes(code)) {
        flush()
        if (MC_COLORS[code]) st = { ...PLAIN, color: MC_COLORS[code] }
        else if (code === 'l') st = { ...st, b: true }
        else if (code === 'o') st = { ...st, i: true }
        else if (code === 'n') st = { ...st, u: true }
        else if (code === 'm') st = { ...st, s: true }
        else if (code === 'k') st = { ...st, k: true }
        else if (code === 'r') st = { ...PLAIN }
        i++
        continue
      }
    }
    buf += ch
  }
  flush()
  return out
}

export const stripMotd = (text: string | null | undefined) => (text || '').replace(/[§&][0-9a-fk-or]/gi, '')

export function Motd({ text }: { text: string | null | undefined }) {
  return (
    <>
      {parseMotd(text || '').map((p, i) => (
        <span
          key={i}
          className={p.k ? 'mc-k' : undefined}
          style={{
            color: p.color || undefined,
            fontWeight: p.b ? 700 : undefined,
            fontStyle: p.i ? 'italic' : undefined,
            textDecoration: [p.u && 'underline', p.s && 'line-through'].filter(Boolean).join(' ') || undefined,
          }}
        >
          {p.text}
        </span>
      ))}
    </>
  )
}
