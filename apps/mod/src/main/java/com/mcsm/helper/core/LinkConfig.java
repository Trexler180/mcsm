package com.mcsm.helper.core;

import java.net.URI;
import java.util.Optional;

/**
 * Connection settings handed to the mod by the agent that spawned the server
 * process. See PROTOCOL.md §1.
 *
 * <p>Absence is not an error. A server started outside the panel simply has no
 * agent to talk to, and the mod must behave exactly as if it were not installed.
 */
public final class LinkConfig {

	public static final String ENV_URL = "MCSM_AGENT_URL";
	public static final String ENV_SERVER_ID = "MCSM_SERVER_ID";
	public static final String ENV_TOKEN = "MCSM_TOKEN";

	private final URI endpoint;
	private final String serverId;
	private final String token;

	private LinkConfig(URI endpoint, String serverId, String token) {
		this.endpoint = endpoint;
		this.serverId = serverId;
		this.token = token;
	}

	/**
	 * Reads the configuration from the environment, or returns empty when this
	 * process was not started by an agent.
	 */
	public static Optional<LinkConfig> fromEnvironment() {
		return fromValues(System.getenv(ENV_URL), System.getenv(ENV_SERVER_ID), System.getenv(ENV_TOKEN));
	}

	/** Visible for testing. */
	public static Optional<LinkConfig> fromValues(String baseUrl, String serverId, String token) {
		if (isBlank(baseUrl) || isBlank(serverId) || isBlank(token)) {
			return Optional.empty();
		}

		URI base;
		try {
			base = URI.create(baseUrl.trim());
		} catch (IllegalArgumentException e) {
			return Optional.empty();
		}

		String scheme = switch (String.valueOf(base.getScheme()).toLowerCase()) {
			case "http", "ws" -> "ws";
			case "https", "wss" -> "wss";
			default -> null;
		};
		if (scheme == null || base.getHost() == null) {
			return Optional.empty();
		}

		// The server id is interpolated into a URL path, so it is restricted to
		// characters that cannot alter the path's structure.
		if (!serverId.matches("[A-Za-z0-9_-]{1,64}")) {
			return Optional.empty();
		}

		String path = trimTrailingSlash(base.getRawPath()) + "/agent/v1/link/" + serverId;
		URI endpoint;
		try {
			endpoint = new URI(scheme, null, base.getHost(), base.getPort(), path, null, null);
		} catch (Exception e) {
			return Optional.empty();
		}

		return Optional.of(new LinkConfig(endpoint, serverId, token.trim()));
	}

	public URI endpoint() {
		return endpoint;
	}

	public String serverId() {
		return serverId;
	}

	/** The bearer token. Never log this value. */
	public String token() {
		return token;
	}

	private static boolean isBlank(String s) {
		return s == null || s.trim().isEmpty();
	}

	private static String trimTrailingSlash(String s) {
		if (s == null || s.isEmpty()) {
			return "";
		}
		return s.endsWith("/") ? s.substring(0, s.length() - 1) : s;
	}

	/** Deliberately omits the token so it cannot leak through a log statement. */
	@Override
	public String toString() {
		return "LinkConfig[endpoint=" + endpoint + ", serverId=" + serverId + "]";
	}
}
