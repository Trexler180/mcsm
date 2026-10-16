import { describe, expect, it } from 'vitest'
import type { ConfigNode } from './model'
import { parseConfig, saveConfig } from './index'

const find = (n: ConfigNode, ...path: (string | number)[]): ConfigNode | undefined =>
  path.reduce<ConfigNode | undefined>((at, k) => at?.children?.find((c) => (typeof k === 'number' ? c.index === k : c.key === k)), n)

// The shape of Paper's paper-global.yml, which lists items at their key's own
// indent and goes on with more keys afterwards.
const PAPER = `# Paper's global settings
_version: 29
anticheat:
  obfuscation:
    items:
      all-models:
        also-obfuscate: []
        dont-obfuscate:
        - minecraft:lodestone_tracker
        - minecraft:custom_name
        sanitize-count: true
      enable-item-obfuscation: false
misc:
  # How many players can join per tick
  max-joins-per-tick: 5
`

describe('YAML configs', () => {
  it('reads a list written at its key’s indent, and everything after it', () => {
    const parsed = parseConfig('paper-global.yml', PAPER)
    expect(parsed).not.toBeNull()
    const root = parsed!.root
    const list = find(root, 'anticheat', 'obfuscation', 'items', 'all-models', 'dont-obfuscate')
    expect(list?.type).toBe('array')
    expect(list?.children?.map((c) => c.value)).toEqual(['minecraft:lodestone_tracker', 'minecraft:custom_name'])
    expect(find(root, 'anticheat', 'obfuscation', 'items', 'all-models', 'sanitize-count')?.value).toBe(true)
    expect(find(root, 'anticheat', 'obfuscation', 'items', 'enable-item-obfuscation')?.value).toBe(false)
    expect(find(root, 'misc', 'max-joins-per-tick')?.value).toBe(5)
  })

  it('keeps comments and layout when a value is changed', () => {
    const parsed = parseConfig('paper-global.yml', PAPER)!
    const joins = find(parsed.root, 'misc', 'max-joins-per-tick')!
    joins.value = 8
    const saved = saveConfig(parsed, PAPER)
    expect(saved).toBe(PAPER.replace('max-joins-per-tick: 5', 'max-joins-per-tick: 8'))
  })

  it('reads a compact list of maps', () => {
    const text = `worlds:\n- name: world\n  seed: 42\n- name: nether\n  seed: 7\nafter: yes\n`
    const root = parseConfig('worlds.yml', text)!.root
    const worlds = find(root, 'worlds')
    expect(worlds?.type).toBe('array')
    expect(worlds?.children?.map((w) => find(w, 'name')?.value)).toEqual(['world', 'nether'])
    expect(find(root, 'after')?.value).toBe('yes')
  })

  it('still reads an empty key as nothing', () => {
    const root = parseConfig('x.yml', `empty:\nnext: 1\n`)!.root
    expect(find(root, 'empty')?.type).toBe('null')
    expect(find(root, 'next')?.value).toBe(1)
  })
})
