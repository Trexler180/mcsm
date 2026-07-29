package com.mcsm.helper.core;

import java.net.http.HttpClient;
import java.net.http.WebSocket;
import java.time.Duration;
import java.util.List;
import java.util.Locale;
import java.util.concurrent.CompletionStage;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.ThreadLocalRandom;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.function.Consumer;

/**
 * Owns the WebSocket link to the agent: handshake, heartbeat, reconnection and
 * frame dispatch.
 *
 * <p>Everything here runs on the client's own threads. No method on this class
 * blocks a game thread — {@link #enqueueEvent} and {@link #enqueueSnapshot} only
 * touch the bounded queue, which never waits.
 *
 * <p>Uses {@link java.net.http.WebSocket} from the JDK, so the mod ships with no
 * bundled networking library.
 */
public final class LinkClient {

	/** Close codes that mean retrying cannot possibly help (PROTOCOL.md §1). */
	private static final int CLOSE_BAD_VERSION = 4400;
	private static final int CLOSE_UNAUTHORIZED = 4401;
	private static final int CLOSE_DUPLICATE = 4409;

	private static final long BACKOFF_MIN_MS = 1_000L;
	private static final long BACKOFF_MAX_MS = 60_000L;
	private static final long DEFAULT_HEARTBEAT_MS = 15_000L;
	private static final long RPC_TIMEOUT_MS = 10_000L;
	private static final int QUEUE_CAPACITY = 256;

	private final LinkConfig config;
	private final ServerFacade facade;
	private final Logger log;

	private final OutboundQueue queue = new OutboundQueue(QUEUE_CAPACITY);
	private final AtomicBoolean running = new AtomicBoolean(false);
	private final ScheduledExecutorService scheduler =
			Executors.newSingleThreadScheduledExecutor(r -> thread(r, "mcsm-helper-sched"));

	private volatile Thread loopThread;
	private volatile WebSocket socket;
	private volatile boolean sessionAlive;
	private volatile long heartbeatMs = DEFAULT_HEARTBEAT_MS;
	private volatile ScheduledFuture<?> heartbeatTask;
	private long backoffMs = BACKOFF_MIN_MS;

	private final RpcDispatcher dispatcher;

	/** Minimal logging seam so the core does not depend on a platform logger. */
	public interface Logger {
		void info(String message);

		void warn(String message);

		void debug(String message);
	}

	public LinkClient(LinkConfig config, ServerFacade facade, Logger log) {
		this.config = config;
		this.facade = facade;
		this.log = log;
		this.dispatcher = new RpcDispatcher(facade, RPC_TIMEOUT_MS, new RpcDispatcher.Clock() {
			@Override
			public long nowMillis() {
				return System.currentTimeMillis();
			}

			@Override
			public void schedule(long delayMillis, Runnable task) {
				try {
					scheduler.schedule(task, delayMillis, TimeUnit.MILLISECONDS);
				} catch (Exception ignored) {
					// Scheduler shutting down; the link is going away anyway.
				}
			}
		});
	}

	// ── Lifecycle ───────────────────────────────────────────────────────────

	public void start() {
		if (!running.compareAndSet(false, true)) {
			return;
		}
		Thread t = thread(this::runLoop, "mcsm-helper-link");
		loopThread = t;
		t.start();
	}

	public void stop() {
		if (!running.compareAndSet(true, false)) {
			return;
		}
		cancelHeartbeat();
		closeSocketQuietly(WebSocket.NORMAL_CLOSURE, "server stopping");
		scheduler.shutdownNow();
		Thread t = loopThread;
		if (t != null) {
			t.interrupt();
		}
	}

	// ── Producer API — safe from the server thread ──────────────────────────

	/** Events are droppable: the next snapshot re-establishes truth. */
	public void enqueueEvent(Messages.Event event) {
		enqueue(Frames.encode(Messages.TYPE_EVENT, System.currentTimeMillis(), event), true);
	}

	/** Snapshots carry correctness and are never dropped for an event. */
	public void enqueueSnapshot(Messages.Snapshot snapshot) {
		enqueue(Frames.encode(Messages.TYPE_SNAPSHOT, System.currentTimeMillis(), snapshot), false);
	}

	private void enqueue(String payload, boolean droppable) {
		if (!running.get()) {
			return;
		}
		queue.offer(payload, droppable);
	}

	// ── Connection loop ─────────────────────────────────────────────────────

	private void runLoop() {
		while (running.get()) {
			try {
				connectAndServe();
			} catch (InterruptedException e) {
				Thread.currentThread().interrupt();
				return;
			} catch (PermanentFailure e) {
				log.warn("helper link disabled: " + e.getMessage());
				running.set(false);
				return;
			} catch (Exception e) {
				log.debug("helper link error: " + e);
			}

			if (!running.get()) {
				return;
			}
			sleepBackoff();
		}
	}

	private void connectAndServe() throws Exception {
		HttpClient http = HttpClient.newBuilder()
				.connectTimeout(Duration.ofSeconds(10))
				.build();

		Handler handler = new Handler();
		WebSocket ws;
		try {
			ws = http.newWebSocketBuilder()
					.header("Authorization", "Bearer " + config.token())
					.connectTimeout(Duration.ofSeconds(10))
					.buildAsync(config.endpoint(), handler)
					.get(15, TimeUnit.SECONDS);
		} catch (Exception e) {
			// An agent that is not up yet is the normal case during boot, so this
			// is debug rather than a warning the user would see and worry about.
			log.debug("helper link connect failed: " + rootMessage(e));
			throw e;
		}

		socket = ws;
		sessionAlive = true;
		queue.clear();

		sendHello();

		// Drain until the session ends. take() wakes periodically so a closed
		// session is noticed even when there is nothing to send.
		while (running.get() && sessionAlive) {
			OutboundQueue.Item item = queue.take(500);
			if (item == null) {
				continue;
			}
			try {
				ws.sendText(item.payload(), true).get(15, TimeUnit.SECONDS);
			} catch (Exception e) {
				log.debug("helper link send failed: " + rootMessage(e));
				sessionAlive = false;
			}
		}

		cancelHeartbeat();
		closeSocketQuietly(WebSocket.NORMAL_CLOSURE, "closing");
		if (handler.permanent != null) {
			throw new PermanentFailure(handler.permanent);
		}
	}

	private void sendHello() {
		Messages.Hello hello = new Messages.Hello();
		hello.modVersion = facade.modVersion();
		hello.mcVersion = facade.mcVersion();
		hello.loader = facade.loader();
		hello.loaderVersion = facade.loaderVersion();
		hello.serverBrand = facade.serverBrand();
		hello.capabilities = List.of("vitals", "player_events", "rpc", "command_exec");
		// Bypasses the queue's drop policy by being first in an empty queue.
		enqueue(Frames.encode(Messages.TYPE_HELLO, System.currentTimeMillis(), hello), false);
	}

	// ── Frame handling ──────────────────────────────────────────────────────

	private void onFrame(String text) {
		Frames.Envelope env = Frames.decode(text);
		if (env == null) {
			log.debug("helper link: ignoring unparseable frame");
			return;
		}
		if (env.v != Frames.PROTOCOL_VERSION) {
			log.debug("helper link: ignoring frame with protocol version " + env.v);
			return;
		}

		switch (env.type) {
			case Messages.TYPE_WELCOME -> onWelcome(env);
			case Messages.TYPE_RPC_REQUEST -> onRpcRequest(env);
			default -> log.debug("helper link: ignoring unknown frame type " + env.type);
		}
	}

	private void onWelcome(Frames.Envelope env) {
		Messages.Welcome welcome = Frames.payload(env, Messages.Welcome.class);
		long interval = welcome != null && welcome.heartbeatMs > 0 ? welcome.heartbeatMs : DEFAULT_HEARTBEAT_MS;
		// Clamped so a bad or hostile value cannot make the server sample itself
		// to death, nor stall the panel's view indefinitely.
		heartbeatMs = Math.min(Math.max(interval, 1_000L), 300_000L);
		backoffMs = BACKOFF_MIN_MS;
		log.info("helper link established (heartbeat " + heartbeatMs + "ms)");
		startHeartbeat();
		sampleAndEnqueueSnapshot();
	}

	private void onRpcRequest(Frames.Envelope env) {
		Messages.RpcRequest request = Frames.payload(env, Messages.RpcRequest.class);
		String id = env.id;
		if (id == null) {
			log.debug("helper link: rpc_request without id, ignoring");
			return;
		}
		dispatcher.dispatch(id, request, frame -> enqueue(frame, false));
	}

	// ── Heartbeat ───────────────────────────────────────────────────────────

	private void startHeartbeat() {
		cancelHeartbeat();
		try {
			heartbeatTask = scheduler.scheduleAtFixedRate(
					this::sampleAndEnqueueSnapshot, heartbeatMs, heartbeatMs, TimeUnit.MILLISECONDS);
		} catch (Exception e) {
			log.debug("helper link: could not schedule heartbeat: " + e);
		}
	}

	private void cancelHeartbeat() {
		ScheduledFuture<?> task = heartbeatTask;
		if (task != null) {
			task.cancel(false);
			heartbeatTask = null;
		}
	}

	/** Hops to the server thread to sample, then hands the result back to the queue. */
	private void sampleAndEnqueueSnapshot() {
		if (!running.get() || !sessionAlive) {
			return;
		}
		facade.submit(() -> {
			try {
				Messages.Snapshot snapshot = facade.sampleSnapshot();
				if (snapshot != null) {
					enqueueSnapshot(snapshot);
				}
			} catch (Throwable t) {
				log.debug("helper link: snapshot sampling failed: " + t);
			}
		});
	}

	// ── Plumbing ────────────────────────────────────────────────────────────

	private final class Handler implements WebSocket.Listener {
		private final StringBuilder buffer = new StringBuilder();
		volatile String permanent;

		@Override
		public void onOpen(WebSocket webSocket) {
			webSocket.request(1);
		}

		@Override
		public CompletionStage<?> onText(WebSocket webSocket, CharSequence data, boolean last) {
			buffer.append(data);
			if (last) {
				String text = buffer.toString();
				buffer.setLength(0);
				try {
					onFrame(text);
				} catch (Throwable t) {
					log.debug("helper link: frame handling failed: " + t);
				}
			}
			webSocket.request(1);
			return null;
		}

		@Override
		public CompletionStage<?> onClose(WebSocket webSocket, int statusCode, String reason) {
			sessionAlive = false;
			if (statusCode == CLOSE_BAD_VERSION || statusCode == CLOSE_UNAUTHORIZED || statusCode == CLOSE_DUPLICATE) {
				permanent = describe(statusCode, reason);
			} else {
				log.debug("helper link closed (" + statusCode + " " + reason + ")");
			}
			return null;
		}

		@Override
		public void onError(WebSocket webSocket, Throwable error) {
			sessionAlive = false;
			log.debug("helper link error: " + rootMessage(error));
		}
	}

	private static String describe(int code, String reason) {
		String meaning = switch (code) {
			case CLOSE_BAD_VERSION -> "agent does not support this protocol version";
			case CLOSE_UNAUTHORIZED -> "agent rejected the launch token";
			case CLOSE_DUPLICATE -> "another live session already exists for this server";
			default -> "closed";
		};
		return meaning + (reason == null || reason.isEmpty() ? "" : " (" + reason + ")");
	}

	private void closeSocketQuietly(int code, String reason) {
		WebSocket ws = socket;
		socket = null;
		sessionAlive = false;
		if (ws == null) {
			return;
		}
		try {
			ws.sendClose(code, reason);
		} catch (Exception ignored) {
			// Already gone.
		}
	}

	private void sleepBackoff() {
		long jitter = ThreadLocalRandom.current().nextLong(0, Math.max(1, backoffMs / 4));
		long delay = Math.min(backoffMs + jitter, BACKOFF_MAX_MS);
		try {
			Thread.sleep(delay);
		} catch (InterruptedException e) {
			Thread.currentThread().interrupt();
			return;
		}
		backoffMs = Math.min(backoffMs * 2, BACKOFF_MAX_MS);
	}

	private static String rootMessage(Throwable t) {
		Throwable cause = t;
		while (cause.getCause() != null && cause.getCause() != cause) {
			cause = cause.getCause();
		}
		String message = cause.getMessage();
		return message == null ? cause.getClass().getSimpleName() : message;
	}

	private static Thread thread(Runnable body, String name) {
		Thread t = new Thread(body, name);
		// Daemon: a lingering link thread must never keep a stopping server alive.
		t.setDaemon(true);
		t.setUncaughtExceptionHandler((thr, err) ->
				System.err.println("[mcsm-helper] uncaught in " + thr.getName() + ": " + err));
		return t;
	}

	/** Signals a failure that retrying cannot fix. */
	private static final class PermanentFailure extends RuntimeException {
		PermanentFailure(String message) {
			super(message);
		}
	}

	@SuppressWarnings("unused")
	private static String lower(String s) {
		return s == null ? "" : s.toLowerCase(Locale.ROOT);
	}

	/** Diagnostics for the log line the panel shows. */
	public long droppedFrames() {
		return queue.droppedCount();
	}
}
