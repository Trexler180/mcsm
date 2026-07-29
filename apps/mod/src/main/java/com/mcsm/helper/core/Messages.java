package com.mcsm.helper.core;

import java.util.ArrayList;
import java.util.List;

/**
 * Wire types for the link protocol (PROTOCOL.md §3).
 *
 * <p>These are plain classes with public fields rather than records: Gson
 * serialises records only from 2.10 onward, and the Gson version is whatever
 * Minecraft happens to bundle. Plain fields work on every version.
 *
 * <p>Field names are camelCase and converted to snake_case on the wire by the
 * naming policy configured in {@link Frames}. The fixtures in {@code fixtures/}
 * are the authority on the exact wire shape.
 */
public final class Messages {

	private Messages() {
	}

	// ── Frame types ─────────────────────────────────────────────────────────

	public static final String TYPE_HELLO = "hello";
	public static final String TYPE_WELCOME = "welcome";
	public static final String TYPE_SNAPSHOT = "snapshot";
	public static final String TYPE_EVENT = "event";
	public static final String TYPE_RPC_REQUEST = "rpc_request";
	public static final String TYPE_RPC_RESPONSE = "rpc_response";

	// ── Event kinds ─────────────────────────────────────────────────────────

	public static final String EVENT_PLAYER_JOIN = "player_join";
	public static final String EVENT_PLAYER_LEAVE = "player_leave";
	public static final String EVENT_PLAYER_DEATH = "player_death";
	public static final String EVENT_SERVER_READY = "server_ready";
	public static final String EVENT_SERVER_STOPPING = "server_stopping";

	// ── Error codes ─────────────────────────────────────────────────────────

	public static final String ERR_UNKNOWN_METHOD = "unknown_method";
	public static final String ERR_INVALID_PARAMS = "invalid_params";
	public static final String ERR_PLAYER_NOT_FOUND = "player_not_found";
	public static final String ERR_TIMEOUT = "timeout";
	public static final String ERR_UNSUPPORTED = "unsupported";
	public static final String ERR_INTERNAL = "internal";

	// ── Payloads ────────────────────────────────────────────────────────────

	public static final class Hello {
		public String modVersion;
		public String mcVersion;
		public String loader;
		public String loaderVersion;
		public String serverBrand;
		public List<String> capabilities = new ArrayList<>();
	}

	public static final class Welcome {
		public long heartbeatMs;
		public List<String> acceptedCapabilities = new ArrayList<>();
		public long maxFrameBytes;
	}

	public static final class Tps {
		public double m1;
		public double m5;
		public double m15;
	}

	public static final class Mspt {
		public double avg;
		public double p50;
		public double p95;
		public double p99;
		public double max;
	}

	public static final class Heap {
		public long usedMb;
		public long committedMb;
		public long maxMb;
	}

	public static final class Gc {
		public long collections;
		public long timeMs;
	}

	public static final class Chunks {
		public long loaded;
	}

	public static final class Entities {
		public long total;
	}

	public static final class Dimension {
		public String id;
		public long chunks;
		public long entities;
	}

	public static final class PlayerInfo {
		public String uuid;
		public String name;
		public int pingMs;
		public String dimension;
	}

	public static final class Players {
		public int online;
		public int max;
		public List<PlayerInfo> list = new ArrayList<>();
	}

	public static final class Snapshot {
		public long uptimeMs;
		public Tps tps;
		public Mspt mspt;
		public Heap heap;
		public Gc gc;
		public Chunks chunks;
		public Entities entities;
		public List<Dimension> dimensions = new ArrayList<>();
		public Players players;
	}

	public static final class Event {
		public String kind;
		public String uuid;
		public String name;
		public String message;

		public static Event player(String kind, String uuid, String name) {
			Event e = new Event();
			e.kind = kind;
			e.uuid = uuid;
			e.name = name;
			return e;
		}

		public static Event simple(String kind) {
			Event e = new Event();
			e.kind = kind;
			return e;
		}
	}

	public static final class RpcRequest {
		public String method;
		public com.google.gson.JsonObject params;
	}

	public static final class RpcError {
		public String code;
		public String message;

		public RpcError() {
		}

		public RpcError(String code, String message) {
			this.code = code;
			this.message = message;
		}
	}

	public static final class RpcResponse {
		public boolean ok;
		public com.google.gson.JsonElement result;
		public RpcError error;
	}
}
