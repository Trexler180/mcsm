import { describe, expect, it } from 'vitest'
import { OPERATION_KIND_LABELS } from './api'
import { OPERATION_TEXT } from '@/pages/server/header'
import { alertLink } from '@/pages/alerts'
import typesSrc from './types.ts?raw'
import alertsSrc from '@/pages/alerts.tsx?raw'

// The web app keeps its own copies of names the API defines: statuses, kinds,
// permissions, verdicts. Nothing ties the copies to the originals, and when one
// drifts the page doesn't break, it just says the wrong thing (the
// version-change preview read two verdicts the wrong way round). These tests
// read the Go source and hold the copies to it.

// Every non-test Go file in the API, by path relative to apps/api.
const goSources = Object.fromEntries(
  Object.entries(import.meta.glob(['../../../api/internal/**/*.go', '!**/*_test.go'], { query: '?raw', import: 'default', eager: true })).map(
    ([path, src]) => [path.replace(/^(\.\.\/)+api\//, ''), src as string],
  ),
)
const api = (rel: string) => {
  const src = goSources[rel]
  if (src === undefined) throw new Error(`${rel} not found among ${Object.keys(goSources).length} API sources`)
  return src
}

/** Every capture of re in src. */
const all = (src: string, re: RegExp) => [...src.matchAll(re)].map((m) => m[1])

/** The string literals in a TypeScript union type: `export type Name = "a" | "b"`. */
function union(src: string, name: string): string[] {
  const m = src.match(new RegExp(`export type ${name}\\s*=([^;]+);`))
  if (!m) throw new Error(`type ${name} not found`)
  return all(m[1], /["']([^"']+)["']/g)
}

/** The string literals of a property's inline union inside an interface. */
function fieldUnion(src: string, iface: string, field: string): string[] {
  const body = src.match(new RegExp(`export interface ${iface} \\{([\\s\\S]*?)\\n\\}`))
  if (!body) throw new Error(`interface ${iface} not found`)
  const m = body[1].match(new RegExp(`\\n\\s*${field}:([^;]+);`))
  if (!m) throw new Error(`${iface}.${field} not found`)
  return all(m[1], /["']([^"']+)["']/g)
}


const types = typesSrc

describe('names the web app shares with the API', () => {
  it('has words for every kind of operation', () => {
    const kinds = all(api('internal/operations/operations.go'), /\bKind\w+\s*=\s*"([a-z_]+)"/g)
    expect(kinds.length).toBeGreaterThan(5)
    for (const k of kinds) {
      expect(OPERATION_KIND_LABELS, `busy message for ${k}`).toHaveProperty(k)
      expect(OPERATION_TEXT, `header text for ${k}`).toHaveProperty(k)
    }
  })

  it('reads the version-check verdicts as the API means them', () => {
    const src = api('internal/api/handlers/mods_updates.go')
    const verdicts = all(src, /\bcompat\w+\s*=\s*"([a-z]+)"/g)
    expect(union(types, 'ModCompatStatus').sort()).toEqual([...verdicts].sort())
    // The one with a build to move to is "compatible", the unchanged one "supported".
    expect(src).toMatch(/compatCompatible\s*=\s*"compatible"\s*\/\/ a newer\/other build exists/)
    expect(src).toMatch(/compatSupported\s*=\s*"supported"\s*\/\/ the installed build already lists/)
  })

  it('knows every server permission and no others', () => {
    const go = all(api('internal/store/models.go'), /ServerPermission\w+\s+ServerPermission\s*=\s*"([a-z.]+)"/g)
    const ts = [...union(types, 'ServerPermissionGroup'), ...union(types, 'ServerPermissionLeaf')]
    expect([...ts].sort()).toEqual([...new Set(go)].sort())
  })

  it('knows every status the API gives a server', () => {
    const known = new Set(union(types, 'ServerStatus'))
    const written = new Set<string>()
    for (const src of Object.values(goSources)) {
      for (const s of all(src, /(?:setStatus|SetStatus|UpdateServerStatus\w*)\([^()]*?"([a-z_]+)"\)/g)) written.add(s)
    }
    expect(written.size).toBeGreaterThan(3)
    for (const s of written) expect(known, `server status "${s}"`).toContain(s)
  })

  it('knows every version-change outcome and phase', () => {
    const engine = api('internal/migrate/engine.go')
    const statuses = new Set(all(engine, /\bsave\("([a-z]+)"/g))
    const phases = new Set(all(engine, /\.Phase\s*=\s*"([a-z_]+)"/g))
    const runStatus = fieldUnion(types, 'VersionMigration', 'status')
    const runPhase = fieldUnion(types, 'VersionMigrationDetail', 'phase')
    for (const s of statuses) expect(runStatus, `run status "${s}"`).toContain(s)
    for (const p of phases) expect(runPhase, `run phase "${p}"`).toContain(p)
  })

  it('only links alerts the API can raise', () => {
    const events = new Set(all(api('internal/notify/events.go'), /\bEvent\w+\s*=\s*"([a-z_.]+)"/g))
    const linked = all(alertsSrc, /t === '([a-z_]+\.[a-z_]+)'/g)
    expect(linked.length).toBeGreaterThan(3)
    for (const e of linked) expect(events, `alert type "${e}"`).toContain(e)
    // And each server alert the API raises either links somewhere or is meant not to.
    const item = (event_type: string) => ({ event_type, server_id: 's1' }) as Parameters<typeof alertLink>[0]
    expect(alertLink(item('server.version_change_failed'))).not.toBeNull()
  })
})
