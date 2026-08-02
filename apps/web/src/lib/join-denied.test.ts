import { describe, expect, it } from "vitest";
import { asJoinDeniedAlert } from "./join-denied";
import type { NotificationItem } from "./types";

function item(over: Partial<NotificationItem>): NotificationItem {
  return {
    id: "n1",
    user_id: "u1",
    event_type: "player.join_denied",
    severity: "warning",
    server_id: "srv1",
    node_id: null,
    title: "Steve tried to join Survival",
    body: "Steve is not on the whitelist and could not join.",
    data: { player: "Steve", uuid: "069a79f4", bedrock: false, attempts: 1 },
    dedupe_key: "player.join_denied:srv1:steve",
    created_at: "2026-08-01T12:00:00Z",
    read_at: null,
    ...over,
  };
}

describe("asJoinDeniedAlert", () => {
  it("reads the payload of a join-denied alert", () => {
    expect(asJoinDeniedAlert(item({}))).toEqual({
      serverId: "srv1",
      player: "Steve",
      uuid: "069a79f4",
      bedrock: false,
      attempts: 1,
    });
  });

  it("carries the Bedrock flag, which selects a different whitelist path", () => {
    const alert = asJoinDeniedAlert(
      item({ data: { player: ".CoolGuy", uuid: "0000-0009", bedrock: true, attempts: 2 } }),
    );
    expect(alert).toMatchObject({ player: ".CoolGuy", bedrock: true, attempts: 2 });
  });

  it("ignores other event types", () => {
    expect(asJoinDeniedAlert(item({ event_type: "server.crash" }))).toBeNull();
  });

  // Without a server there is nothing to whitelist on, and without a name there
  // is nobody to whitelist — in both cases the button must not appear at all
  // rather than appear and fail.
  it("rejects an alert missing what the action needs", () => {
    expect(asJoinDeniedAlert(item({ server_id: null }))).toBeNull();
    expect(asJoinDeniedAlert(item({ data: {} }))).toBeNull();
    expect(asJoinDeniedAlert(item({ data: { player: "" } }))).toBeNull();
    expect(asJoinDeniedAlert(item({ data: { player: 42 } }))).toBeNull();
  });

  it("defaults the optional fields rather than trusting their shape", () => {
    const alert = asJoinDeniedAlert(
      item({ data: { player: "Steve", uuid: null, bedrock: "yes", attempts: "many" } }),
    );
    expect(alert).toEqual({
      serverId: "srv1",
      player: "Steve",
      uuid: "",
      bedrock: false,
      attempts: 1,
    });
  });
});
