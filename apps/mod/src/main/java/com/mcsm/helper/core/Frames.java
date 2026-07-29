package com.mcsm.helper.core;

import com.google.gson.FieldNamingPolicy;
import com.google.gson.Gson;
import com.google.gson.GsonBuilder;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonSyntaxException;

/**
 * Envelope encoding and decoding (PROTOCOL.md §2).
 *
 * <p>Gson is used rather than a bundled JSON library because Minecraft already
 * ships it — the helper jar is embedded in the agent binary and committed to
 * git, so every avoided dependency is real weight saved.
 */
public final class Frames {

	public static final int PROTOCOL_VERSION = 1;

	private static final Gson GSON = new GsonBuilder()
			.setFieldNamingPolicy(FieldNamingPolicy.LOWER_CASE_WITH_UNDERSCORES)
			.disableHtmlEscaping()
			.create();

	private Frames() {
	}

	/** The protocol envelope. Unknown fields are ignored by Gson, as required. */
	public static final class Envelope {
		public int v;
		public String type;
		public long ts;
		public String id;
		public JsonElement data;
	}

	public static Gson gson() {
		return GSON;
	}

	/** Encodes a frame with no correlation id. */
	public static String encode(String type, long ts, Object payload) {
		return encode(type, null, ts, payload);
	}

	/** Encodes a frame, optionally carrying a correlation id. */
	public static String encode(String type, String id, long ts, Object payload) {
		JsonObject root = new JsonObject();
		root.addProperty("v", PROTOCOL_VERSION);
		root.addProperty("type", type);
		root.addProperty("ts", ts);
		if (id != null) {
			root.addProperty("id", id);
		}
		root.add("data", payload == null ? new JsonObject() : GSON.toJsonTree(payload));
		return GSON.toJson(root);
	}

	/**
	 * Parses an envelope.
	 *
	 * @return the envelope, or null when the text is not a usable frame. A
	 *         malformed frame is ignored rather than fatal — one bad frame must
	 *         not take down a link.
	 */
	public static Envelope decode(String text) {
		try {
			Envelope env = GSON.fromJson(text, Envelope.class);
			if (env == null || env.type == null) {
				return null;
			}
			return env;
		} catch (JsonSyntaxException e) {
			return null;
		}
	}

	/** Converts an envelope's data payload into a concrete type, or null. */
	public static <T> T payload(Envelope env, Class<T> type) {
		if (env == null || env.data == null || !env.data.isJsonObject()) {
			return null;
		}
		try {
			return GSON.fromJson(env.data, type);
		} catch (JsonSyntaxException e) {
			return null;
		}
	}
}
