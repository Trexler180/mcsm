package com.mcsm.helper.core;

import com.google.gson.JsonObject;

import java.util.List;
import java.util.function.Consumer;

/**
 * Routes an {@code rpc_request} to the facade and produces exactly one
 * {@code rpc_response} (PROTOCOL.md §3.6, §4).
 *
 * <p>The threading rule from PROTOCOL.md §5 is enforced here rather than left to
 * each method: the calling (IO) thread never waits. Work is scheduled onto the
 * server thread and the reply is delivered asynchronously by correlation id. A
 * watchdog guarantees a reply even if the server thread is wedged — a request
 * that never gets answered would leak agent-side state, so a late {@code timeout}
 * is strictly better than silence.
 */
public final class RpcDispatcher {

	private final ServerFacade facade;
	private final long timeoutMillis;
	private final Clock clock;

	/** Indirection so tests can drive time without sleeping. */
	public interface Clock {
		long nowMillis();

		void schedule(long delayMillis, Runnable task);
	}

	public RpcDispatcher(ServerFacade facade, long timeoutMillis, Clock clock) {
		this.facade = facade;
		this.timeoutMillis = timeoutMillis;
		this.clock = clock;
	}

	/**
	 * Handles one request. Returns immediately; {@code reply} is invoked exactly
	 * once, on whichever thread completes first.
	 *
	 * @param reply receives the encoded {@code rpc_response} frame
	 */
	public void dispatch(String id, Messages.RpcRequest request, Consumer<String> reply) {
		// Latch so the watchdog and the real result cannot both answer.
		final boolean[] answered = {false};
		final Object lock = new Object();

		Consumer<Messages.RpcResponse> answerOnce = response -> {
			synchronized (lock) {
				if (answered[0]) {
					return;
				}
				answered[0] = true;
			}
			reply.accept(Frames.encode(Messages.TYPE_RPC_RESPONSE, id, clock.nowMillis(), response));
		};

		if (request == null || request.method == null || request.method.isEmpty()) {
			answerOnce.accept(error(Messages.ERR_INVALID_PARAMS, "missing method"));
			return;
		}

		clock.schedule(timeoutMillis, () -> answerOnce.accept(
				error(Messages.ERR_TIMEOUT, "server thread did not complete the request in time")));

		facade.submit(() -> {
			Messages.RpcResponse response;
			try {
				response = invoke(request.method, request.params == null ? new JsonObject() : request.params);
			} catch (Throwable t) {
				// A failing RPC must not take down the server thread.
				response = error(Messages.ERR_INTERNAL, String.valueOf(t.getMessage()));
			}
			answerOnce.accept(response);
		});
	}

	/** Runs on the server thread. */
	private Messages.RpcResponse invoke(String method, JsonObject params) {
		switch (method) {
			case "player.kick": {
				String player = requireString(params, "player");
				if (player == null) {
					return error(Messages.ERR_INVALID_PARAMS, "player is required");
				}
				return facade.kick(player, optionalString(params, "reason", ""))
						? ok()
						: error(Messages.ERR_PLAYER_NOT_FOUND, "no player named " + player + " is online");
			}
			case "player.ban": {
				String player = requireString(params, "player");
				if (player == null) {
					return error(Messages.ERR_INVALID_PARAMS, "player is required");
				}
				return facade.ban(player, optionalString(params, "reason", ""))
						? ok()
						: error(Messages.ERR_PLAYER_NOT_FOUND, "no player named " + player);
			}
			case "player.pardon": {
				String player = requireString(params, "player");
				if (player == null) {
					return error(Messages.ERR_INVALID_PARAMS, "player is required");
				}
				return facade.pardon(player) ? ok() : error(Messages.ERR_PLAYER_NOT_FOUND, "not banned: " + player);
			}
			case "whitelist.add": {
				String player = requireString(params, "player");
				if (player == null) {
					return error(Messages.ERR_INVALID_PARAMS, "player is required");
				}
				return facade.whitelistAdd(player) ? ok() : error(Messages.ERR_PLAYER_NOT_FOUND, "unknown: " + player);
			}
			case "whitelist.remove": {
				String player = requireString(params, "player");
				if (player == null) {
					return error(Messages.ERR_INVALID_PARAMS, "player is required");
				}
				return facade.whitelistRemove(player) ? ok() : error(Messages.ERR_PLAYER_NOT_FOUND, "unknown: " + player);
			}
			case "whitelist.list": {
				List<String> players = facade.whitelistList();
				JsonObject result = new JsonObject();
				result.add("players", Frames.gson().toJsonTree(players));
				return ok(result);
			}
			case "op.grant": {
				String player = requireString(params, "player");
				if (player == null) {
					return error(Messages.ERR_INVALID_PARAMS, "player is required");
				}
				return facade.op(player) ? ok() : error(Messages.ERR_PLAYER_NOT_FOUND, "unknown: " + player);
			}
			case "op.revoke": {
				String player = requireString(params, "player");
				if (player == null) {
					return error(Messages.ERR_INVALID_PARAMS, "player is required");
				}
				return facade.deop(player) ? ok() : error(Messages.ERR_PLAYER_NOT_FOUND, "unknown: " + player);
			}
			case "message.broadcast": {
				String message = requireString(params, "message");
				if (message == null) {
					return error(Messages.ERR_INVALID_PARAMS, "message is required");
				}
				facade.broadcast(message);
				return ok();
			}
			case "message.player": {
				String player = requireString(params, "player");
				String message = requireString(params, "message");
				if (player == null || message == null) {
					return error(Messages.ERR_INVALID_PARAMS, "player and message are required");
				}
				return facade.messagePlayer(player, message)
						? ok()
						: error(Messages.ERR_PLAYER_NOT_FOUND, "no player named " + player + " is online");
			}
			case "server.save": {
				facade.save(params.has("flush") && params.get("flush").getAsBoolean());
				return ok();
			}
			case "server.stop": {
				facade.stop();
				return ok();
			}
			case "command.exec": {
				String command = requireString(params, "command");
				if (command == null) {
					return error(Messages.ERR_INVALID_PARAMS, "command is required");
				}
				ServerFacade.ExecResult result = facade.exec(command);
				JsonObject payload = new JsonObject();
				payload.add("output", Frames.gson().toJsonTree(result.output()));
				payload.addProperty("success", result.success());
				return ok(payload);
			}
			default:
				return error(Messages.ERR_UNKNOWN_METHOD, "no such method: " + method);
		}
	}

	private static String requireString(JsonObject params, String key) {
		if (params == null || !params.has(key) || params.get(key).isJsonNull()) {
			return null;
		}
		String value = params.get(key).getAsString();
		return value.isEmpty() ? null : value;
	}

	private static String optionalString(JsonObject params, String key, String fallback) {
		String value = requireString(params, key);
		return value == null ? fallback : value;
	}

	private static Messages.RpcResponse ok() {
		return ok(new JsonObject());
	}

	private static Messages.RpcResponse ok(JsonObject result) {
		Messages.RpcResponse response = new Messages.RpcResponse();
		response.ok = true;
		response.result = result;
		return response;
	}

	private static Messages.RpcResponse error(String code, String message) {
		Messages.RpcResponse response = new Messages.RpcResponse();
		response.ok = false;
		response.error = new Messages.RpcError(code, message);
		return response;
	}
}
