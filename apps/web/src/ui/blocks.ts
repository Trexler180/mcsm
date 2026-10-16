// Hand-drawn 8x8 block faces that stand in for a server's icon until it has
// its own server-icon.png. Each letter is a palette entry. Generated from the
// redesign prototype.

export interface BlockDef {
  label: string
  pal: Record<string, string>
  rows: string[]
}

export const BLOCKS = {
  grass: { label: 'Grass block', pal: { G: '#6aad44', g: '#5a9a38', d: '#8a5d3b', D: '#6e4a2e' }, rows: [
    'GGGGGGGG', 'GgGGgGGg', 'GdGgdGdG', 'ddgdDddd', 'dDddddDd', 'ddDdDddd', 'dddddDdd', 'dDddddDd'] },
  stone: { label: 'Stone', pal: { s: '#8b8b8b', S: '#727272', L: '#a0a0a0' }, rows: [
    'ssSsssss', 'sssssLss', 'sLssSsss', 'ssssssSs', 'sSsLssss', 'ssssssLs', 'ssLsSsss', 'Ssssssss'] },
  diamond_ore: { label: 'Diamond ore', pal: { s: '#8b8b8b', S: '#727272', L: '#a0a0a0', C: '#6ff0f7', c: '#22a9b4' }, rows: [
    'ssSsssss', 'sCcsssLs', 'scCsSsss', 'sssssCcs', 'sSsLscCs', 'ssCcssss', 'ssccSsSs', 'Ssssssss'] },
  tnt: { label: 'TNT', pal: { R: '#db3b2f', r: '#a92b22', W: '#ececec', k: '#2a2a2a' }, rows: [
    'RrRrRrRr', 'RrRrRrRr', 'RrRrRrRr', 'WkkWkWkk', 'WWkWkkWk', 'RrRrRrRr', 'RrRrRrRr', 'RrRrRrRr'] },
  furnace: { label: 'Lit furnace', pal: { c: '#8e8e8e', C: '#6f6f6f', d: '#3a3a3a', k: '#1e1e1e', F: '#ffbe3b', f: '#f0631d' }, rows: [
    'cCcCcCcC', 'CddddddC', 'cCcCcCcC', 'CcCcCcCc', 'cCkkkkCc', 'CckFfkcC', 'cCkfFkCc', 'CcCcCcCc'] },
  command: { label: 'Command block', pal: { O: '#d9853b', o: '#b0662a', k: '#353535', w: '#e2e2e2' }, rows: [
    'OOoOOoOO', 'OkkkkkkO', 'okwkkkko', 'OkkwkkkO', 'OkwkkkkO', 'okkkwwko', 'OkkkkkkO', 'OOoOOoOO'] },
  bricks: { label: 'Bricks', pal: { b: '#a24d3c', B: '#8a3f31', m: '#cbbcae' }, rows: [
    'bbbmbbbB', 'bBbmbbbb', 'mmmmmmmm', 'bmbbbBbm', 'bmBbbbbm', 'mmmmmmmm', 'bbbmbBbb', 'bBbmbbbb'] },
  island: { label: 'Sky island', pal: { s: '#8ec5f0', L: '#3f8f3a', l: '#337a2f', T: '#6b4a2b', G: '#5fae3e', D: '#8a5d3b', d: '#6e4a2e' }, rows: [
    'sssLlsss', 'ssLLLlss', 'sssTssss', 'sGGTGGGs', 'sDdDDdDs', 'ssDDdDss', 'sssdDsss', 'ssssssss'] },
  heart: { label: 'Hardcore heart', pal: { k: '#1c1a1d', R: '#d42a2a', r: '#9e1c1c', w: '#ffb8b8' }, rows: [
    'kkkkkkkk', 'kRRkkRRk', 'RwRRRRRr', 'RRRRRRRr', 'kRRRRRrk', 'kkRRRrkk', 'kkkRrkkk', 'kkkkkkkk'] },
  bookshelf: { label: 'Bookshelf', pal: { W: '#b48a52', w: '#94703f', r: '#a33b2f', b: '#3a5ba3', g: '#3f8a3f', y: '#c9a33a' }, rows: [
    'WwWWwWWw', 'rbgyWbrg', 'rbgyWbrg', 'WwWWwWWw', 'gyrbbgWy', 'gyrbbgWy', 'gyrbbgWy', 'WwWWwWWw'] },
  pumpkin: { label: 'Jack o’lantern', pal: { O: '#e07b1f', o: '#bd6418', y: '#ffd640' }, rows: [
    'OoOOoOOo', 'OoOOoOOo', 'OyyOoyyo', 'OoyOoyOo', 'OoOOoOOo', 'OyyyyyyO', 'OoyOyoyo', 'OoOOoOOo'] },
  log: { label: 'Oak log', pal: { B: '#6b5233', l: '#c09a5b', d: '#9c7a45' }, rows: [
    'BBBBBBBB', 'BllllllB', 'BlddddlB', 'BldlldlB', 'BldlldlB', 'BlddddlB', 'BllllllB', 'BBBBBBBB'] },
  gold: { label: 'Gold block', pal: { H: '#fff2a3', G: '#f5d34a', g: '#e8c03a', D: '#c99a1e' }, rows: [
    'HHHHHHHD', 'HGGgGGGD', 'HGgGGgGD', 'HGGGgGGD', 'HgGGGGgD', 'HGGgGGGD', 'HGgGGgGD', 'DDDDDDDD'] },
  ice: { label: 'Packed ice', pal: { i: '#a5c9f7', I: '#8db5ec', w: '#d6e8fd' }, rows: [
    'iiIiiwii', 'iwiiIiii', 'iiiwiiIi', 'Iiiiiwii', 'iiIiiiiw', 'iwiiIiii', 'iiiwiiiI', 'Iiiiiwii'] },
} satisfies Record<string, BlockDef>

export type BlockKind = keyof typeof BLOCKS
export const BLOCK_KINDS = Object.keys(BLOCKS) as BlockKind[]
