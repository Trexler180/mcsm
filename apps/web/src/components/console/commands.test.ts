import { describe, expect, it } from "vitest";
import { matchCommands } from "./commands";

describe("matchCommands", () => {
  it("matches by prefix, with or without a leading slash", () => {
    expect(matchCommands("whi").map((c) => c.cmd)).toEqual(["whitelist"]);
    expect(matchCommands("/whi").map((c) => c.cmd)).toEqual(["whitelist"]);
  });

  it("is case-insensitive", () => {
    expect(matchCommands("GAMEM")[0].cmd).toBe("gamemode");
  });

  it("returns nothing once arguments start", () => {
    expect(matchCommands("whitelist add")).toEqual([]);
  });

  it("returns nothing for empty input or an exact command", () => {
    expect(matchCommands("")).toEqual([]);
    expect(matchCommands("list")).toEqual([]);
  });

  it("returns multiple candidates for a shared prefix", () => {
    const cmds = matchCommands("ban").map((c) => c.cmd);
    expect(cmds).toContain("ban-ip");
    expect(cmds).toContain("banlist");
    expect(cmds).not.toContain("ban"); // exact match excluded
  });
});
