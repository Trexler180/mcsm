import { describe, expect, it } from "vitest";
import {
  actionPhrase,
  asMcpActionAlert,
  resolvedRequestId,
} from "./mcp-action-alert";
import {
  EVENT_MCP_ACTION_REQUESTED,
  EVENT_MCP_ACTION_RESOLVED,
  EVENT_PLAYER_JOIN_DENIED,
  type NotificationItem,
} from "./types";

function item(over: Partial<NotificationItem> = {}): NotificationItem {
  return {
    id: "n1",
    user_id: "u1",
    event_type: EVENT_MCP_ACTION_REQUESTED,
    severity: "warning",
    server_id: "srv-1",
    node_id: null,
    title: "Claude Code wants to restart survival",
    body: "Approve or deny this before it expires.",
    data: {
      request_id: "req-1",
      action: "restart",
      client_name: "Claude Code",
      server_name: "survival",
      reason: "TPS has been under 5 for ten minutes.",
      expires_at: "2026-08-15T19:44:58Z",
      requires_password: true,
    },
    dedupe_key: "mcp.action_requested:req-1",
    created_at: new Date().toISOString(),
    read_at: null,
    ...over,
  } as NotificationItem;
}

describe("asMcpActionAlert", () => {
  it("reads a well-formed prompt", () => {
    expect(asMcpActionAlert(item())).toEqual({
      requestId: "req-1",
      serverId: "srv-1",
      serverName: "survival",
      action: "restart",
      clientName: "Claude Code",
      reason: "TPS has been under 5 for ten minutes.",
      requiresPassword: true,
      expiresAt: "2026-08-15T19:44:58Z",
    });
  });

  it("ignores other kinds of alert", () => {
    expect(asMcpActionAlert(item({ event_type: EVENT_PLAYER_JOIN_DENIED }))).toBeNull();
    expect(asMcpActionAlert(item({ event_type: EVENT_MCP_ACTION_RESOLVED }))).toBeNull();
  });

  // A pair of approve/deny buttons that cannot say which request they would
  // answer must not be rendered at all.
  it("refuses a payload that could not identify the request", () => {
    expect(asMcpActionAlert(item({ data: {} }))).toBeNull();
    expect(asMcpActionAlert(item({ data: { request_id: "" } }))).toBeNull();
    expect(asMcpActionAlert(item({ data: { request_id: "req-1" } }))).toBeNull();
    expect(asMcpActionAlert(item({ data: { request_id: 7, action: "restart" } }))).toBeNull();
    expect(asMcpActionAlert(item({ server_id: null }))).toBeNull();
  });

  // Erring the other way would offer a one-click approve the API then refuses,
  // which reads to the user as a broken button.
  it("assumes a step-up is needed when the payload does not say", () => {
    const alert = asMcpActionAlert(
      item({ data: { request_id: "req-1", action: "restart" } }),
    );
    expect(alert?.requiresPassword).toBe(true);
  });

  it("takes an explicit false at its word", () => {
    const alert = asMcpActionAlert(
      item({
        data: { request_id: "req-1", action: "restart", requires_password: false },
      }),
    );
    expect(alert?.requiresPassword).toBe(false);
  });

  it("falls back to a usable client name", () => {
    const alert = asMcpActionAlert(
      item({ data: { request_id: "req-1", action: "restart" } }),
    );
    expect(alert?.clientName).toBe("An agent");
  });
});

describe("resolvedRequestId", () => {
  it("names the request a resolution refers to", () => {
    expect(
      resolvedRequestId(
        item({
          event_type: EVENT_MCP_ACTION_RESOLVED,
          data: { request_id: "req-1", status: "executed" },
        }),
      ),
    ).toBe("req-1");
  });

  it("is null for a prompt or a malformed resolution", () => {
    expect(resolvedRequestId(item())).toBeNull();
    expect(
      resolvedRequestId(item({ event_type: EVENT_MCP_ACTION_RESOLVED, data: {} })),
    ).toBeNull();
  });
});

describe("actionPhrase", () => {
  it("renders an upgrade so it reads in a sentence", () => {
    expect(actionPhrase("upgrade:26.1.2")).toBe("upgrade to 26.1.2");
  });

  it("leaves the lifecycle verbs alone", () => {
    expect(actionPhrase("restart")).toBe("restart");
  });
});
