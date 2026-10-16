// The server software the panel runs, as people name it, and what its add-ons
// are called.

export const PLATFORMS: Record<string, { label: string; content: 'plugin' | 'mod' | 'datapack' }> = {
  vanilla: { label: 'Vanilla', content: 'datapack' },
  paper: { label: 'Paper', content: 'plugin' },
  purpur: { label: 'Purpur', content: 'plugin' },
  spigot: { label: 'Spigot', content: 'plugin' },
  fabric: { label: 'Fabric', content: 'mod' },
  quilt: { label: 'Quilt', content: 'mod' },
  forge: { label: 'Forge', content: 'mod' },
  neoforge: { label: 'NeoForge', content: 'mod' },
}

export function platformLabel(p: string): string {
  return PLATFORMS[p]?.label ?? (p ? p.charAt(0).toUpperCase() + p.slice(1) : '')
}

/** What a server's add-ons are called: Plugins, Mods or Datapacks. */
export function contentLabel(p: string): string {
  const c = PLATFORMS[p]?.content
  return c === 'plugin' ? 'Plugins' : c === 'datapack' ? 'Datapacks' : 'Mods'
}
