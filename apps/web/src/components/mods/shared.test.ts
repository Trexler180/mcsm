import { describe, expect, it } from "vitest";
import type { ModSearchHit, ModVersion } from "@/lib/types";
import { compatible, versionCompatible } from "./shared";

const hit = (overrides: Partial<ModSearchHit>): ModSearchHit =>
  ({ project_id: "p", title: "t", ...overrides }) as ModSearchHit;

describe("compatible", () => {
  it("rejects a hit whose version list lacks the server's MC version", () => {
    const h = hit({ versions: ["1.20.4", "1.21"], categories: ["fabric"] });
    expect(compatible(h, "fabric", "1.21.4", "mod")).toBe(false);
  });

  it("accepts a matching MC version and loader", () => {
    const h = hit({ versions: ["1.21.4"], categories: ["fabric", "utility"] });
    expect(compatible(h, "fabric", "1.21.4", "mod")).toBe(true);
  });

  it("skips the MC gate when the hit lists no versions", () => {
    const h = hit({ versions: [], categories: ["fabric"] });
    expect(compatible(h, "fabric", "1.21.4", "mod")).toBe(true);
  });

  it("skips the MC gate when the caller passes no server version (SpigotMC)", () => {
    const h = hit({ versions: ["1.8.9"], categories: [] });
    expect(compatible(h, "", "", "plugin")).toBe(true);
  });

  it("rejects a mod whose categories lack the server loader", () => {
    const h = hit({ versions: ["1.21.4"], categories: ["forge"] });
    expect(compatible(h, "fabric", "1.21.4", "mod")).toBe(false);
  });

  it("only gates on loader for project type mod", () => {
    const h = hit({ versions: ["1.21.4"], categories: [] });
    expect(compatible(h, "fabric", "1.21.4", "datapack")).toBe(true);
  });

  it("skips the loader gate when no server loader is passed (CurseForge)", () => {
    const h = hit({ versions: ["1.21.4"], categories: [] });
    expect(compatible(h, "", "1.21.4", "mod")).toBe(true);
  });
});

describe("versionCompatible", () => {
  const version = (overrides: Partial<ModVersion>): ModVersion =>
    ({ id: "v", loaders: [], game_versions: [], ...overrides }) as ModVersion;

  it("matches loader case-insensitively", () => {
    const v = version({ loaders: ["Fabric"], game_versions: ["1.21.4"] });
    expect(versionCompatible(v, "fabric", "1.21.4")).toBe(true);
  });

  it("rejects a wrong game version", () => {
    const v = version({ loaders: ["fabric"], game_versions: ["1.20.1"] });
    expect(versionCompatible(v, "fabric", "1.21.4")).toBe(false);
  });

  it("skips the loader gate when the version declares no loaders", () => {
    const v = version({ loaders: [], game_versions: ["1.21.4"] });
    expect(versionCompatible(v, "fabric", "1.21.4")).toBe(true);
  });

  it("rejects a loader mismatch", () => {
    const v = version({ loaders: ["forge"], game_versions: ["1.21.4"] });
    expect(versionCompatible(v, "fabric", "1.21.4")).toBe(false);
  });
});
