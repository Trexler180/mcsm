import { describe, expect, it } from "vitest";
import { groupServersByFolder, folderAccent } from "./folders";
import type { ServerFolder } from "./types";

function folder(over: Partial<ServerFolder> & { id: string }): ServerFolder {
  return {
    name: over.id,
    description: "",
    color: "",
    sort_order: 0,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    server_count: 0,
    ...over,
  };
}

const srv = (id: string, folder_id: string | null) => ({ id, folder_id });

describe("groupServersByFolder", () => {
  it("buckets servers into their folders and keeps the API's folder order", () => {
    const folders = [folder({ id: "mini" }), folder({ id: "staging" })];
    const servers = [
      srv("bedwars", "mini"),
      srv("canary", "staging"),
      srv("skywars", "mini"),
    ];

    const groups = groupServersByFolder(servers, folders);

    expect(groups.map((g) => g.folder?.id)).toEqual(["mini", "staging"]);
    expect(groups[0].servers.map((s) => s.id)).toEqual(["bedwars", "skywars"]);
    expect(groups[1].servers.map((s) => s.id)).toEqual(["canary"]);
  });

  it("puts ungrouped servers in a trailing null-folder section", () => {
    const groups = groupServersByFolder(
      [srv("survival", null), srv("bedwars", "mini")],
      [folder({ id: "mini" })],
    );

    expect(groups).toHaveLength(2);
    expect(groups[1].folder).toBeNull();
    expect(groups[1].servers.map((s) => s.id)).toEqual(["survival"]);
  });

  it("omits the ungrouped section entirely when every server is filed", () => {
    const groups = groupServersByFolder(
      [srv("bedwars", "mini")],
      [folder({ id: "mini" })],
    );

    expect(groups).toHaveLength(1);
    expect(groups[0].folder?.id).toBe("mini");
  });

  it("keeps empty folders so a newly created one is visible and droppable", () => {
    const groups = groupServersByFolder([], [folder({ id: "mini" })]);

    expect(groups).toHaveLength(1);
    expect(groups[0].servers).toEqual([]);
  });

  // A collaborator can hold one server out of a folder they can't otherwise
  // see; the server must still appear rather than disappear from the list.
  it("shows a server whose folder is not visible instead of dropping it", () => {
    const groups = groupServersByFolder([srv("bedwars", "hidden")], []);

    expect(groups).toHaveLength(1);
    expect(groups[0].folder).toBeNull();
    expect(groups[0].servers.map((s) => s.id)).toEqual(["bedwars"]);
  });

  it("preserves the incoming server order within a folder", () => {
    const groups = groupServersByFolder(
      [srv("c", "mini"), srv("a", "mini"), srv("b", "mini")],
      [folder({ id: "mini" })],
    );

    expect(groups[0].servers.map((s) => s.id)).toEqual(["c", "a", "b"]);
  });
});

describe("folderAccent", () => {
  it("returns the palette entry for a known color", () => {
    expect(folderAccent("blue").dot).toContain("blue");
  });

  it("falls back to the neutral accent for empty or unknown colors", () => {
    const neutral = folderAccent("");
    expect(folderAccent(undefined)).toEqual(neutral);
    expect(folderAccent("chartreuse")).toEqual(neutral);
  });
});
