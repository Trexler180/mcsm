import { api } from "./api";
import { can } from "./permissions";
import {
  EVENT_PLAYER_JOIN_DENIED,
  type JoinDeniedData,
  type NotificationItem,
} from "./types";

// Shared logic behind the "someone tried to join" prompt, used by both places it
// appears: the live toast and the notification inbox. Keeping the payload
// reading and the whitelist call together means the two surfaces cannot drift
// into disagreeing about who is being let in.

/** A join-denied alert with its payload validated. */
export interface JoinDeniedAlert {
  serverId: string;
  player: string;
  uuid: string;
  bedrock: boolean;
  attempts: number;
}

/** Reads a notification as a join-denied alert, or null if it is any other kind
 *  — or is one whose payload lacks what an action would need. The payload is
 *  server-built, but it is still untyped JSON by the time it reaches here, and a
 *  button that cannot say who it would whitelist must not be rendered. */
export function asJoinDeniedAlert(
  item: NotificationItem,
): JoinDeniedAlert | null {
  if (item.event_type !== EVENT_PLAYER_JOIN_DENIED || !item.server_id) {
    return null;
  }
  const data = item.data as Partial<JoinDeniedData>;
  if (typeof data?.player !== "string" || data.player === "") return null;
  return {
    serverId: item.server_id,
    player: data.player,
    uuid: typeof data.uuid === "string" ? data.uuid : "",
    bedrock: data.bedrock === true,
    attempts: typeof data.attempts === "number" ? data.attempts : 1,
  };
}

/** Whether this user may whitelist on the alert's server. The API enforces this
 *  regardless; asking first is what keeps a button that is certain to 403 off
 *  the screen. A failed check reads as "no", since offering an action we could
 *  not confirm is worse than omitting it. */
export async function canWhitelistOn(serverId: string): Promise<boolean> {
  try {
    const perms = await api.servers.myPermissions(serverId);
    return can(perms, "players.whitelist");
  } catch {
    return false;
  }
}

/** Adds the player to the server's whitelist.
 *
 *  A Bedrock player is flagged so the agent writes whitelist.json against their
 *  Floodgate UUID: the console command would have the server ask Mojang about a
 *  name Mojang has never heard of. No gamertag is passed — the UUID came from
 *  the join attempt itself, so there is nothing left to resolve. */
export function whitelistFromAlert(alert: JoinDeniedAlert): Promise<unknown> {
  return api.players.action(alert.serverId, {
    action: "whitelist_add",
    name: alert.player,
    uuid: alert.uuid || undefined,
    bedrock: alert.bedrock || undefined,
  });
}
