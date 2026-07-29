package com.mcsm.helper.core;

import java.util.List;

/**
 * Everything the platform-agnostic core needs from the game server.
 *
 * <p>This is the seam that makes a future Paper or NeoForge adapter an addition
 * rather than a rewrite: the protocol, queueing, backoff, auth and dispatch code
 * never mentions Minecraft or Fabric, and a new platform only has to implement
 * this interface.
 *
 * <p><b>Threading:</b> every method except {@link #submit} and the static
 * identity accessors is called <em>on the server thread</em>, scheduled via
 * {@link #submit}. Implementations may therefore touch game state directly and
 * must not do their own thread hopping.
 */
public interface ServerFacade {

	/** Result of running a command, including what it printed back. */
	record ExecResult(List<String> output, boolean success) {
	}

	// ── Identity (safe to call from any thread) ─────────────────────────────

	String modVersion();

	String mcVersion();

	String loader();

	String loaderVersion();

	String serverBrand();

	/**
	 * Schedules work on the server thread. Must never block the caller — this is
	 * invoked from the IO thread and, indirectly, from the tick thread.
	 */
	void submit(Runnable task);

	// ── Sampling (server thread) ────────────────────────────────────────────

	/**
	 * Builds a full state snapshot. Called on the server thread, so it must be
	 * cheap: read pre-aggregated counters, never walk every entity on demand.
	 */
	Messages.Snapshot sampleSnapshot();

	// ── RPC operations (server thread) ──────────────────────────────────────

	/** @return false when the named player is not online. */
	boolean kick(String player, String reason);

	boolean ban(String player, String reason);

	boolean pardon(String player);

	boolean whitelistAdd(String player);

	boolean whitelistRemove(String player);

	List<String> whitelistList();

	boolean op(String player);

	boolean deop(String player);

	void broadcast(String message);

	/** @return false when the named player is not online. */
	boolean messagePlayer(String player, String message);

	void save(boolean flush);

	void stop();

	ExecResult exec(String command);
}
