// Placeholder item visuals — no bundled texture atlas. Items are color-coded by
// material category with a rarity ring, and we compute durability locally from a
// small built-in table so tiles can show a wear bar without any assets.

export type ItemCategory =
  | 'weapon'
  | 'tool'
  | 'armor'
  | 'food'
  | 'block'
  | 'redstone'
  | 'brewing'
  | 'valuable'
  | 'misc'

export type ItemRarity = 'common' | 'uncommon' | 'rare' | 'epic'

// "minecraft:diamond_sword" -> "Diamond Sword"
export function itemLabel(id: string): string {
  return id
    .replace(/^minecraft:/, '')
    .split('_')
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(' ')
}

// A short token for the slot tile (first letters of each word, max 3 chars).
export function itemAbbr(id: string): string {
  const name = id.replace(/^minecraft:/, '')
  const parts = name.split('_')
  if (parts.length === 1) return name.slice(0, 3)
  return parts
    .map((p) => p[0])
    .join('')
    .slice(0, 3)
}

function bare(id: string): string {
  return id.replace(/^minecraft:/, '')
}

const WEAPON_SUFFIXES = ['sword', 'axe', 'bow', 'crossbow', 'trident', 'mace']
const TOOL_SUFFIXES = [
  'pickaxe',
  'shovel',
  'hoe',
  'shears',
  'fishing_rod',
  'flint_and_steel',
  'brush',
]
const ARMOR_SUFFIXES = ['helmet', 'chestplate', 'leggings', 'boots']

const FOOD = new Set([
  'apple',
  'bread',
  'cooked_beef',
  'cooked_porkchop',
  'cooked_chicken',
  'cooked_mutton',
  'cooked_rabbit',
  'cooked_cod',
  'cooked_salmon',
  'beef',
  'porkchop',
  'chicken',
  'mutton',
  'rabbit',
  'cod',
  'salmon',
  'carrot',
  'potato',
  'baked_potato',
  'beetroot',
  'beetroot_soup',
  'mushroom_stew',
  'rabbit_stew',
  'melon_slice',
  'sweet_berries',
  'glow_berries',
  'golden_carrot',
  'golden_apple',
  'enchanted_golden_apple',
  'cookie',
  'pumpkin_pie',
  'cake',
  'dried_kelp',
  'honey_bottle',
  'milk_bucket',
  'rotten_flesh',
  'spider_eye',
  'chorus_fruit',
  'suspicious_stew',
])

const VALUABLE = new Set([
  'diamond',
  'emerald',
  'gold_ingot',
  'iron_ingot',
  'netherite_ingot',
  'netherite_scrap',
  'gold_nugget',
  'iron_nugget',
  'copper_ingot',
  'lapis_lazuli',
  'amethyst_shard',
  'quartz',
  'nether_star',
  'heart_of_the_sea',
  'echo_shard',
  'ancient_debris',
])

const BREWING = new Set([
  'potion',
  'splash_potion',
  'lingering_potion',
  'experience_bottle',
  'glass_bottle',
  'dragon_breath',
  'fermented_spider_eye',
  'blaze_powder',
  'nether_wart',
  'glistering_melon_slice',
  'ghast_tear',
  'phantom_membrane',
])

const REDSTONE = new Set([
  'redstone',
  'repeater',
  'comparator',
  'piston',
  'sticky_piston',
  'observer',
  'dropper',
  'dispenser',
  'hopper',
  'redstone_torch',
  'redstone_block',
  'lever',
  'tripwire_hook',
  'daylight_detector',
  'target',
  'note_block',
  'redstone_lamp',
])

const EPIC = new Set([
  'nether_star',
  'dragon_egg',
  'dragon_head',
  'enchanted_golden_apple',
  'elytra',
  'totem_of_undying',
  'end_crystal',
  'heart_of_the_sea',
  'beacon',
  'conduit',
  'echo_shard',
  'recovery_compass',
])

const RARE = new Set([
  'golden_apple',
  'enchanted_book',
  'experience_bottle',
  'name_tag',
  'music_disc',
  'trident',
  'nether_star',
])

export function itemCategory(id: string): ItemCategory {
  const name = bare(id)
  if (WEAPON_SUFFIXES.some((s) => name.endsWith(s))) return 'weapon'
  if (TOOL_SUFFIXES.some((s) => name.endsWith(s) || name === s)) return 'tool'
  if (ARMOR_SUFFIXES.some((s) => name.endsWith(s))) return 'armor'
  if (FOOD.has(name)) return 'food'
  if (VALUABLE.has(name)) return 'valuable'
  if (BREWING.has(name)) return 'brewing'
  if (REDSTONE.has(name)) return 'redstone'
  if (
    name.endsWith('_block') ||
    name.endsWith('_planks') ||
    name.endsWith('_log') ||
    name.endsWith('_stairs') ||
    name.endsWith('_slab') ||
    name.endsWith('_wall') ||
    name.endsWith('_fence') ||
    name.endsWith('_ore') ||
    name === 'stone' ||
    name === 'dirt' ||
    name === 'cobblestone' ||
    name === 'sand' ||
    name === 'gravel' ||
    name === 'glass'
  )
    return 'block'
  return 'misc'
}

const CATEGORY_BG: Record<ItemCategory, string> = {
  weapon: 'bg-red-500/15 text-red-300',
  tool: 'bg-amber-500/15 text-amber-300',
  armor: 'bg-sky-500/15 text-sky-300',
  food: 'bg-green-500/15 text-green-300',
  block: 'bg-stone-500/15 text-stone-300',
  redstone: 'bg-rose-500/15 text-rose-300',
  brewing: 'bg-fuchsia-500/15 text-fuchsia-300',
  valuable: 'bg-cyan-500/15 text-cyan-300',
  misc: 'bg-surface-2 text-text-secondary',
}

export function itemRarity(id: string, enchanted: boolean): ItemRarity {
  const name = bare(id)
  if (EPIC.has(name)) return 'epic'
  if (name.startsWith('netherite_')) return 'rare'
  if (RARE.has(name) || name.startsWith('music_disc')) return 'rare'
  // Any enchantment glint nudges an otherwise-common item to uncommon.
  if (enchanted) return 'uncommon'
  return 'common'
}

const RARITY_RING: Record<ItemRarity, string> = {
  common: 'ring-1 ring-inset ring-border/50',
  uncommon: 'ring-1 ring-inset ring-yellow-400/60',
  rare: 'ring-1 ring-inset ring-cyan-400/70',
  epic: 'ring-1 ring-inset ring-fuchsia-400/70',
}

// Background + text + rarity ring for a filled item tile.
export function itemTileClasses(id: string, enchanted: boolean): string {
  const cat = itemCategory(id)
  const rarity = itemRarity(id, enchanted)
  return `${CATEGORY_BG[cat]} ${RARITY_RING[rarity]}`
}

const ROMAN = ['', 'I', 'II', 'III', 'IV', 'V', 'VI', 'VII', 'VIII', 'IX', 'X']

function roman(n: number): string {
  if (n >= 1 && n <= 10) return ROMAN[n]
  return String(n)
}

// Enchantments whose only level is 1 — vanilla omits the numeral for these.
const SINGLE_LEVEL = new Set([
  'mending',
  'flame',
  'infinity',
  'channeling',
  'multishot',
  'silk_touch',
  'aqua_affinity',
  'curse_of_vanishing',
  'curse_of_binding',
  'binding_curse',
  'vanishing_curse',
])

// "minecraft:sharpness" lvl 5 -> "Sharpness V"; "minecraft:mending" -> "Mending".
export function enchantLabel(id: string, level: number): string {
  const name = bare(id)
  const pretty = itemLabel(id)
  if (level <= 1 && SINGLE_LEVEL.has(name)) return pretty
  return `${pretty} ${roman(level)}`
}

const TOOL_TIER: Record<string, number> = {
  wooden: 59,
  stone: 131,
  golden: 32,
  iron: 250,
  diamond: 1561,
  netherite: 2031,
}

const ARMOR_MATERIAL: Record<string, number> = {
  leather: 5,
  golden: 7,
  chainmail: 15,
  iron: 15,
  diamond: 33,
  netherite: 37,
  turtle: 25,
}

const ARMOR_SLOT: Record<string, number> = {
  helmet: 11,
  chestplate: 16,
  leggings: 15,
  boots: 13,
}

const MISC_DURABILITY: Record<string, number> = {
  bow: 384,
  crossbow: 465,
  trident: 250,
  shield: 336,
  elytra: 432,
  fishing_rod: 64,
  shears: 238,
  flint_and_steel: 64,
  mace: 500,
  carrot_on_a_stick: 25,
  warped_fungus_on_a_stick: 100,
  brush: 64,
}

// Best-effort maximum durability for the common damageable items, so a tile can
// render a wear bar from the stored `damage`. Returns undefined when unknown.
export function maxDurability(id: string): number | undefined {
  const name = bare(id)
  if (name in MISC_DURABILITY) return MISC_DURABILITY[name]

  const parts = name.split('_')
  const material = parts[0]
  const kind = parts.slice(1).join('_')

  // Tools/weapons that follow the tiered durability scale.
  if (
    material in TOOL_TIER &&
    (kind === 'sword' ||
      kind === 'pickaxe' ||
      kind === 'axe' ||
      kind === 'shovel' ||
      kind === 'hoe')
  ) {
    return TOOL_TIER[material]
  }

  // Armor: material base × per-slot factor.
  if (material in ARMOR_MATERIAL && kind in ARMOR_SLOT) {
    return ARMOR_MATERIAL[material] * ARMOR_SLOT[kind]
  }

  return undefined
}
