// Targeted scenario tests for the editor's new structural / toggling behaviors.
// Run: npx tsx scripts/test-scenarios.mts
import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
  parseConfig,
  saveConfig,
  valueToTree,
  reindex,
  type ConfigNode,
} from "../src/lib/config/index.ts";

const DIR = "../../servers/fabric-test/config";
let fails = 0;
const check = (name: string, cond: boolean, extra = "") => {
  console.log(`${cond ? "ok  " : "FAIL"} ${name}${extra ? " — " + extra : ""}`);
  if (!cond) fails++;
};

function find(node: ConfigNode, key: string): ConfigNode | undefined {
  if (node.key === key) return node;
  for (const c of node.children ?? []) {
    const r = find(c, key);
    if (r) return r;
  }
  return undefined;
}

// 1. modernfix-mixins.properties: enable a disabled option.
{
  const text = readFileSync(join(DIR, "modernfix-mixins.properties"), "utf8");
  const cfg = parseConfig("modernfix-mixins.properties", text)!;
  const opt = find(cfg.root, "mixin.bugfix.chunk_deadlock");
  check("modernfix: disabled option parsed", !!opt && opt.disabled === true);
  check("modernfix: trailing comment → doc", !!opt?.doc?.includes("overridden"));
  if (opt) {
    opt.disabled = false; // enable
    const out = saveConfig(cfg, text);
    const line = out
      .split("\n")
      .find((l) => l.trimStart().startsWith("mixin.bugfix.chunk_deadlock"));
    check("modernfix: line uncommented", !!line && !line.trimStart().startsWith("#"), line);
    check("modernfix: trailing # stripped", !!line && !line.includes("#"), line);
    const re = parseConfig("modernfix-mixins.properties", out)!;
    const reopt = find(re.root, "mixin.bugfix.chunk_deadlock");
    check("modernfix: re-parse active", reopt?.disabled === false);
  }
}

// 2. almanac-nbtFix.json5: add an item to blacklistItems, comments preserved.
{
  const name = "almanac-nbtFix.json5";
  const text = readFileSync(join(DIR, name), "utf8");
  const cfg = parseConfig(name, text)!;
  const arr = find(cfg.root, "blacklistItems")!;
  check("json5: blacklistItems is array", arr?.type === "array");
  const before = arr.children!.length;
  arr.children!.push(valueToTree("testmod:thing", null, before, []));
  arr.dirty = true;
  reindex(cfg.root, []);
  const out = saveConfig(cfg, text);
  check("json5: comment preserved", out.includes("ItemSplitBugFix"));
  check("json5: new item present", out.includes("testmod:thing"));
  const re = parseConfig(name, out)!;
  check("json5: re-parse array grew", find(re.root, "blacklistItems")!.children!.length === before + 1);
}

// 3. immersive_optimization.json: add + remove keys on the `dimensions` map.
{
  const name = "immersive_optimization.json";
  const text = readFileSync(join(DIR, name), "utf8");
  const cfg = parseConfig(name, text)!;
  const doc = find(cfg.root, "_documentation");
  check("json: _documentation is a URL", /^https?:\/\//.test(String(doc?.value)));
  const dims = find(cfg.root, "dimensions")!;
  const before = dims.children!.length;
  dims.children!.push(valueToTree(true, "mymod:dim", null, []));
  dims.dirty = true;
  reindex(cfg.root, []);
  const out = saveConfig(cfg, text);
  const re = parseConfig(name, out)!;
  check("json: dimension key added", !!find(re.root, "mymod:dim"));
  check("json: other keys intact", find(re.root, "dimensions")!.children!.length === before + 1);
  check("json: re-parse valid + sibling intact", !!find(re.root, "entities"));
}

// 4. recipe_cooldown.cfg: bare scalar value edit.
{
  const name = "recipe_cooldown.cfg";
  const text = readFileSync(join(DIR, name), "utf8");
  const cfg = parseConfig(name, text);
  check("cfg: parsed as scalar", cfg?.format === "scalar" && cfg.root.type === "number");
  if (cfg) {
    cfg.root.value = 99;
    const out = saveConfig(cfg, text);
    check("cfg: value written", out.trim() === "99", JSON.stringify(out));
  }
}

// 5. HOCON (.conf): edit a value in place, comments preserved.
{
  const name = "default.conf";
  const text = readFileSync(join(DIR, "TabTPS/display-configs", name), "utf8");
  const cfg = parseConfig(name, text)!;
  check("hocon: parsed", cfg?.format === "hocon");
  const fill = find(cfg.root, "fill-mode");
  check("hocon: nested key found", !!fill && String(fill.value) === "MSPT");
  if (fill) {
    fill.value = "TPS";
    const out = saveConfig(cfg, text);
    check("hocon: value changed", out.includes("fill-mode=TPS"));
    check("hocon: comment preserved", out.includes("Possible values: [MSPT"));
    const re = parseConfig(name, out)!;
    check("hocon: re-parse value", String(find(re.root, "fill-mode")?.value) === "TPS");
  }
}

// 6. Map-like detection signal: namespace:name keys vs fixed schema.
{
  const imm = parseConfig(
    "immersive_optimization.json",
    readFileSync(join(DIR, "immersive_optimization.json"), "utf8"),
  )!;
  const entities = find(imm.root, "entities")!;
  const entityMapLike = (entities.children ?? []).some((c) =>
    (c.key ?? "").includes(":"),
  );
  check("map: immersive entities is map-like (has ns:name keys)", entityMapLike);

  const theme = parseConfig(
    "default.conf",
    readFileSync(join(DIR, "TabTPS/themes/default.conf"), "utf8"),
  )!;
  const scheme = find(theme.root, "color-scheme")!;
  const schemeMapLike = (scheme.children ?? []).some((c) =>
    (c.key ?? "").includes(":"),
  );
  check("map: tabtps color-scheme is NOT map-like (fixed keys)", !schemeMapLike);
}

console.log(`\nfails=${fails}`);
process.exit(fails > 0 ? 1 : 0);
