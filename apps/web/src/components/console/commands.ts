// Common server console commands for autocomplete. Vanilla's stable command
// set plus the handful every Paper/Fabric admin uses; hints show the usual
// argument shape. This is completion candy, not validation — anything typed
// still goes to the server verbatim.
export interface CommandDef {
  cmd: string;
  hint?: string;
}

export const COMMANDS: CommandDef[] = [
  { cmd: "ban", hint: "ban <player> [reason]" },
  { cmd: "ban-ip", hint: "ban-ip <address|player> [reason]" },
  { cmd: "banlist", hint: "banlist [ips|players]" },
  { cmd: "clear", hint: "clear [player] [item]" },
  { cmd: "deop", hint: "deop <player>" },
  { cmd: "difficulty", hint: "difficulty <peaceful|easy|normal|hard>" },
  { cmd: "effect", hint: "effect <give|clear> <player> [effect]" },
  { cmd: "enchant", hint: "enchant <player> <enchantment> [level]" },
  { cmd: "execute", hint: "execute as <entity> run <command>" },
  { cmd: "fill", hint: "fill <from> <to> <block>" },
  { cmd: "gamemode", hint: "gamemode <survival|creative|adventure|spectator> [player]" },
  { cmd: "gamerule", hint: "gamerule <rule> [value]" },
  { cmd: "give", hint: "give <player> <item> [count]" },
  { cmd: "kick", hint: "kick <player> [reason]" },
  { cmd: "kill", hint: "kill [target]" },
  { cmd: "list", hint: "list — show online players" },
  { cmd: "locate", hint: "locate <structure|biome|poi> <name>" },
  { cmd: "msg", hint: "msg <player> <message>" },
  { cmd: "op", hint: "op <player>" },
  { cmd: "pardon", hint: "pardon <player>" },
  { cmd: "pardon-ip", hint: "pardon-ip <address>" },
  { cmd: "reload", hint: "reload — reload datapacks/plugins" },
  { cmd: "save-all", hint: "save-all [flush]" },
  { cmd: "save-off", hint: "save-off — disable autosave" },
  { cmd: "save-on", hint: "save-on — enable autosave" },
  { cmd: "say", hint: "say <message>" },
  { cmd: "seed", hint: "seed — show the world seed" },
  { cmd: "setblock", hint: "setblock <pos> <block>" },
  { cmd: "setworldspawn", hint: "setworldspawn [pos]" },
  { cmd: "spawnpoint", hint: "spawnpoint [player] [pos]" },
  { cmd: "stop", hint: "stop — shut the server down" },
  { cmd: "summon", hint: "summon <entity> [pos]" },
  { cmd: "teleport", hint: "teleport <target> <destination>" },
  { cmd: "tell", hint: "tell <player> <message>" },
  { cmd: "tellraw", hint: "tellraw <player> <json>" },
  { cmd: "time", hint: "time <set|add|query> <value>" },
  { cmd: "title", hint: "title <player> <title|subtitle|actionbar> <json>" },
  { cmd: "tp", hint: "tp <target> <destination>" },
  { cmd: "weather", hint: "weather <clear|rain|thunder> [duration]" },
  { cmd: "whitelist", hint: "whitelist <add|remove|list|on|off> [player]" },
  { cmd: "worldborder", hint: "worldborder <set|add|center|get> [value]" },
  { cmd: "xp", hint: "xp <add|set|query> <player> <amount>" },
];

// matchCommands returns catalog entries whose command starts with the first
// word of the input. Only matches while the user is still typing that first
// word (no completed arguments yet).
export function matchCommands(input: string): CommandDef[] {
  const text = input.replace(/^\//, "");
  if (text === "" || text.includes(" ")) return [];
  const lower = text.toLowerCase();
  return COMMANDS.filter((c) => c.cmd.startsWith(lower) && c.cmd !== lower);
}
