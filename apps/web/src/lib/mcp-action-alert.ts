import { api } from "./api";
import {
  EVENT_MCP_ACTION_REQUESTED,
  EVENT_MCP_ACTION_RESOLVED,
  type NotificationItem,
} from "./types";

// Shared logic behind the "an agent wants to act on a server" prompt, mirroring
// join-denied.ts. Keeping the payload reading and the approve/deny calls
// together means the toast and any other surface cannot drift into disagreeing
// about which request is being answered.

/** A pending action request with its payload validated. */
export interface MCPActionAlert {
  requestId: string;
  serverId: string;
  serverName: string;
  action: string;
  clientName: string;
  /** Model-authored. Displayed as untrusted evidence, never interpreted. */
  reason: string;
  requiresPassword: boolean;
  expiresAt: string;
}

/** Reads a notification as a pending approval prompt, or null if it is any
 *  other kind — or is one whose payload lacks what an action would need.
 *
 *  The payload is server-built but arrives as untyped JSON, and a pair of
 *  approve/deny buttons that cannot say which request they would answer must
 *  not be rendered. */
export function asMcpActionAlert(
  item: NotificationItem,
): MCPActionAlert | null {
  if (item.event_type !== EVENT_MCP_ACTION_REQUESTED || !item.server_id) {
    return null;
  }
  // Read through `unknown` rather than the payload type: this is untyped JSON
  // by the time it reaches here, and the declared shape describes what the
  // server sends, not what actually arrived.
  const data = item.data as Record<string, unknown>;
  const requestId = data?.request_id;
  const action = data?.action;
  if (typeof requestId !== "string" || requestId === "") return null;
  if (typeof action !== "string" || action === "") return null;
  return {
    requestId,
    serverId: item.server_id,
    serverName: typeof data.server_name === "string" ? data.server_name : "",
    action,
    clientName:
      typeof data.client_name === "string" ? data.client_name : "An agent",
    reason: typeof data.reason === "string" ? data.reason : "",
    // Absent reads as "a step-up is needed". Erring the other way would offer a
    // one-click approve the API then refuses, which looks like a broken button.
    requiresPassword: data.requires_password !== false,
    expiresAt: typeof data.expires_at === "string" ? data.expires_at : "",
  };
}

/** The request id a resolution notification refers to, or null. Used to take
 *  down a prompt that was answered on another device or lapsed. */
export function resolvedRequestId(item: NotificationItem): string | null {
  if (item.event_type !== EVENT_MCP_ACTION_RESOLVED) return null;
  const requestId = (item.data as Record<string, unknown>)?.request_id;
  return typeof requestId === "string" && requestId !== "" ? requestId : null;
}

/** Stable toast identity for one request, so the prompt and the resolution that
 *  cancels it agree on what they are talking about. */
export const mcpActionToastKey = (requestId: string) =>
  `mcp-action:${requestId}`;

/** Approves without a step-up. Only correct when the request reported
 *  `requiresPassword: false`; the API re-checks and 401s otherwise. */
export function approveMcpAction(requestId: string): Promise<unknown> {
  return api.mcp.actionRequests.approve(requestId);
}

/** Declines. Never carries credentials — the API asks for none, deliberately. */
export function denyMcpAction(requestId: string): Promise<unknown> {
  return api.mcp.actionRequests.deny(requestId);
}

/** Renders an action id as something that reads in a sentence. "upgrade:26.1.2"
 *  is the one that needs help; the lifecycle verbs already read fine. */
export function actionPhrase(action: string): string {
  return action.startsWith("upgrade:")
    ? `upgrade to ${action.slice("upgrade:".length)}`
    : action;
}
