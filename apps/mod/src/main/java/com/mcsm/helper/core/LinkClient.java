package com.mcsm.helper.core;

import java.net.http.HttpClient;
import java.net.http.WebSocket;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
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

	/** Close codes with protocol meaning (PROTOCOL.md §1). Only the first two are
	 * permanent: a version mismatch or a rejected token cannot be fixed by
	 * retrying. A duplicate session can — the agent evicts sessions that go
	 * silent, so the stale one this connection collided with will be gone within
	 * its idle deadline, and ordinary backoff rides that out. */
	private static final int CLOSE_BAD_VERSION = 4400;
	private static final int CLOSE_UNAUTHORIZED = 4401;
	private static final int CLOSE_DUPLICATE = 4409;

	private static final long BACKOFF_MIN_MS = 1_000L;
	private static final long BACKOFF_MAX_MS = 60_000L;
	private static final long DEFAULT_HEARTBEAT_MS = 15_000L;
	private static final long RPC_TIMEOUT_MS = 10_000L;
	private static final long STOP_DRAIN_MS = 1_000L;
	private static final int QUEUE_CAPACITY = 256;

	/** Frame-size ceiling used until the agent names one in {@code welcome}. Matches
	 * the agent's own default read limit, so an oversized frame is caught here
	 * rather than by the agent closing the session. */
	private static final int DEFAULT_MAX_FRAME_BYTES = 256 * 1024;
	/** Bounds what the negotiated limit may be set to. The floor keeps a hostile or
	 * buggy value from making every frame undeliverable; the ceiling keeps the
	 * inbound reassembly buffer from being an unbounded allocation. */
	private static final int MIN_FRAME_BYTES = 8 * 1024;
	private static final int MAX_FRAME_BYTES_CEILING = 8 * 1024 * 1024;

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
	private volatile int maxFrameBytes = DEFAULT_MAX_FRAME_BYTES;
	private volatile ScheduledFuture<?> heartbeatTask;
	private long backoffMs = BACKOFF_MIN_MS;

	/** Latches the once-per-session warnings that would otherwise repeat on every
	 * heartbeat. PROTOCOL.md §5.5: the log the panel shows must not be spammed by
	 * a condition the user can do nothing about mid-session. */
	private final AtomicBoolean oversizeWarned = new AtomicBoolean(false);
	private final AtomicBoolean unknownTypeWarned = new AtomicBoolean(false);

	/** One client for the life of the mod. Each {@link HttpClient} eagerly starts a
	 * selector thread and an executor that are only released on close or GC, so
	 * building one per reconnect attempt leaks a thread per backoff step through a
	 * long agent outage. Created on the first attempt so a mod that never connects
	 * costs nothing, and shut down when the connection loop exits. */
	private HttpClient http;

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
		// Let the IO thread flush what is already queued — the server_stopping
		// event, chiefly — before the socket is torn down. Closing first would
		// make that event decorative: enqueued on every shutdown, delivered on
		// none. The join is bounded; a wedged send never holds up a stopping
		// server.
		//
		// Only worth waiting for when there is both a live session and something
		// to send on it. Without that guard this runs on the server thread of
		// every shutdown, including the common case where the agent was never
		// reachable and the IO thread is sitting in backoff with nothing to
		// flush — a second and a half added to each stop for no delivery.
		Thread t = loopThread;
		if (t != null && sessionAlive && queue.size() > 0) {
			try {
				t.join(STOP_DRAIN_MS + 500L);
			} catch (InterruptedException e) {
				Thread.currentThread().interrupt();
			}
		}
		closeSocketQuietly(WebSocket.NORMAL_CLOSURE, "server stopping");
		scheduler.shutdownNow();
		if (t != null && t.isAlive()) {
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
		enqueue(encodeSnapshotWithinLimit(snapshot), false);
	}

	/**
	 * Encodes a snapshot, trimming the player list until the frame fits the size
	 * the agent said it would accept.
	 *
	 * <p>Oversizing is not a soft failure on the agent side: exceeding its read
	 * limit closes the session, and since the next heartbeat produces the same
	 * oversized frame the link would reconnect and die again forever, with nothing
	 * in the log to say why. Only {@code players.list} is negotiable — the counts,
	 * vitals and per-dimension rows are what the panel charts, and
	 * {@code players.online} still carries the true total after a trim.
	 */
	private String encodeSnapshotWithinLimit(Messages.Snapshot snapshot) {
		final int limit = maxFrameBytes;
		String frame = Frames.encode(Messages.TYPE_SNAPSHOT, System.currentTimeMillis(), snapshot);
		if (utf8Length(frame) <= limit) {
			return frame;
		}

		List<Messages.PlayerInfo> players = snapshot.players == null ? null : snapshot.players.list;
		int kept = players == null ? 0 : players.size();
		while (kept > 0 && utf8Length(frame) > limit) {
			kept /= 2;
			snapshot.players.list = new ArrayList<>(players.subList(0, kept));
			frame = Frames.encode(Messages.TYPE_SNAPSHOT, System.currentTimeMillis(), snapshot);
		}

		// Still over with no players left means the bulk is elsewhere — a server
		// with an implausible number of dimensions. Those are a nice-to-have
		// breakdown; the totals above them are not.
		if (utf8Length(frame) > limit) {
			snapshot.dimensions = new ArrayList<>();
			frame = Frames.encode(Messages.TYPE_SNAPSHOT, System.currentTimeMillis(), snapshot);
		}

		if (oversizeWarned.compareAndSet(false, true)) {
			// Once per session: this recurs every heartbeat, and the server log is
			// also what the panel shows the user.
			log.warn("helper link: snapshot exceeds the agent's " + limit + " byte frame limit; "
					+ "reporting " + kept + " of " + (players == null ? 0 : players.size()) + " players");
		}
		return frame;
	}

	/** Encoded size of a frame, without allocating the byte array to measure it. */
	private static int utf8Length(String s) {
		int bytes = 0;
		for (int i = 0; i < s.length(); i++) {
			char c = s.charAt(i);
			if (c < 0x80) {
				bytes += 1;
			} else if (c < 0x800) {
				bytes += 2;
			} else if (Character.isHighSurrogate(c)) {
				// A surrogate pair is one 4-byte code point; skip its low half.
				bytes += 4;
				i++;
			} else {
				bytes += 3;
			}
		}
		return bytes;
	}

	private void enqueue(String payload, boolean droppable) {
		if (!running.get()) {
			return;
		}
		queue.offer(payload, droppable);
	}

	// ── Connection loop ─────────────────────────────────────────────────────

	private void runLoop() {
		try {
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
		} finally {
			// Reached on every exit — normal, permanent failure, or interrupt —
			// so the client's selector thread never outlives the link it served.
			shutdownHttpClient();
		}
	}

	/** The shared client, created on first use. Only touched from the IO thread. */
	private HttpClient httpClient() {
		if (http == null) {
			http = HttpClient.newBuilder()
					.connectTimeout(Duration.ofSeconds(10))
					.build();
		}
		return http;
	}

	private void shutdownHttpClient() {
		HttpClient client = http;
		http = null;
		if (client != null) {
			try {
				client.shutdownNow();
			} catch (Exception ignored) {
				// Best effort; the JVM is usually on its way down anyway.
			}
		}
	}

	private void connectAndServe() throws Exception {
		Handler handler = new Handler();
		WebSocket ws;
		try {
			ws = httpClient().newWebSocketBuilder()
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
		// Per session, not per process: a condition that persisted across a
		// reconnect is worth saying again, and saying it once is enough.
		oversizeWarned.set(false);
		unknownTypeWarned.set(false);

		try {
			// Hello is sent directly rather than through the queue, which makes
			// "first frame on the wire" structural: a player event fired on the
			// game thread between clear() and here lands in the queue and waits
			// its turn, instead of racing hello and getting the whole session
			// closed as a bad handshake.
			ws.sendText(helloFrame(), true).get(15, TimeUnit.SECONDS);

			// Drain until the session ends. While running, take() wakes
			// periodically so a closed session is noticed even when idle. Once
			// stop() is called, keep draining only until the queue empties or the
			// grace period lapses — that is what actually delivers the
			// server_stopping event.
			long stopBy = 0L;
			while (sessionAlive) {
				if (!running.get()) {
					if (stopBy == 0L) {
						stopBy = System.nanoTime() + STOP_DRAIN_MS * 1_000_000L;
					}
					if (queue.size() == 0 || System.nanoTime() >= stopBy) {
						break;
					}
				}
				OutboundQueue.Item item = queue.take(running.get() ? 500 : 50);
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
		} finally {
			// Runs on interrupt too, so stop() can never strand a half-open
			// socket or a heartbeat that outlives its session.
			cancelHeartbeat();
			closeSocketQuietly(WebSocket.NORMAL_CLOSURE, "closing");
			// Discard what belonged to the session that just ended, rather than
			// what preceded the one about to begin. Clearing on connect instead
			// threw away every frame produced before the socket opened — which is
			// exactly when server_ready is queued, so it was never once
			// delivered. Clearing here also stops an rpc_response from a dead
			// session reaching the next one, where the agent's per-session
			// correlation ids restart and a stale id can collide with a live call.
			queue.clear();
		}

		if (handler.permanent != null) {
			throw new PermanentFailure(handler.permanent);
		}
	}

	private String helloFrame() {
		Messages.Hello hello = new Messages.Hello();
		hello.modVersion = facade.modVersion();
		hello.mcVersion = facade.mcVersion();
		hello.loader = facade.loader();
		hello.loaderVersion = facade.loaderVersion();
		hello.serverBrand = facade.serverBrand();
		hello.capabilities = List.of("vitals", "player_events", "rpc", "command_exec");
		return Frames.encode(Messages.TYPE_HELLO, System.currentTimeMillis(), hello);
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
			default -> {
				// Rate-limited per PROTOCOL.md §2: an agent shipped ahead of this
				// build may send a type we do not know on every heartbeat, and one
				// line per frame would bury the log it shares with the server.
				if (unknownTypeWarned.compareAndSet(false, true)) {
					log.debug("helper link: ignoring unknown frame type " + env.type);
				}
			}
		}
	}

	private void onWelcome(Frames.Envelope env) {
		Messages.Welcome welcome = Frames.payload(env, Messages.Welcome.class);
		long interval = welcome != null && welcome.heartbeatMs > 0 ? welcome.heartbeatMs : DEFAULT_HEARTBEAT_MS;
		// Clamped so a bad or hostile value cannot make the server sample itself
		// to death, nor stall the panel's view indefinitely.
		heartbeatMs = Math.min(Math.max(interval, 1_000L), 300_000L);
		// The agent states the size it will read; anything larger closes the
		// session rather than being truncated, so honour it in both directions
		// instead of parsing it and hoping.
		long frameLimit = welcome != null && welcome.maxFrameBytes > 0
				? welcome.maxFrameBytes
				: DEFAULT_MAX_FRAME_BYTES;
		maxFrameBytes = (int) Math.min(Math.max(frameLimit, MIN_FRAME_BYTES), MAX_FRAME_BYTES_CEILING);
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
					this::heartbeatTick, heartbeatMs, heartbeatMs, TimeUnit.MILLISECONDS);
		} catch (Exception e) {
			log.debug("helper link: could not schedule heartbeat: " + e);
		}
	}

	/**
	 * The scheduled body, wrapped.
	 *
	 * <p>{@code scheduleAtFixedRate} cancels a repeating task the first time it
	 * throws, and does so silently. Letting anything escape here would stop every
	 * subsequent snapshot for the life of the session — the panel would show
	 * frozen vitals until the agent's idle deadline eventually evicted the link.
	 */
	private void heartbeatTick() {
		try {
			sampleAndEnqueueSnapshot();
		} catch (Throwable t) {
			log.debug("helper link: heartbeat tick failed: " + t);
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
		/** Set once a frame has overrun the limit, so its remaining parts are
		 * discarded instead of being reassembled into a truncated frame. */
		private boolean discarding;
		volatile String permanent;

		@Override
		public void onOpen(WebSocket webSocket) {
			webSocket.request(1);
		}

		@Override
		public CompletionStage<?> onText(WebSocket webSocket, CharSequence data, boolean last) {
			// A fragmented text message arrives in pieces with no declared total,
			// so without a ceiling the peer decides how much of this server's heap
			// to consume. The agent negotiated a frame size; hold it to it.
			if (!discarding && buffer.length() + data.length() > maxFrameBytes) {
				discarding = true;
				buffer.setLength(0);
				log.debug("helper link: dropping an inbound frame over " + maxFrameBytes + " bytes");
			}
			if (!discarding) {
				buffer.append(data);
			}
			if (last) {
				String text = discarding ? null : buffer.toString();
				buffer.setLength(0);
				discarding = false;
				if (text != null) {
					try {
						onFrame(text);
					} catch (Throwable t) {
						log.debug("helper link: frame handling failed: " + t);
					}
				}
			}
			webSocket.request(1);
			return null;
		}

		@Override
		public CompletionStage<?> onClose(WebSocket webSocket, int statusCode, String reason) {
			sessionAlive = false;
			if (statusCode == CLOSE_BAD_VERSION || statusCode == CLOSE_UNAUTHORIZED) {
				permanent = describe(statusCode, reason);
			} else if (statusCode == CLOSE_DUPLICATE) {
				// Transient in practice: our previous connection has not finished
				// tearing down on the agent side. The agent's idle deadline will
				// clear it; giving up here would leave the link dead for the rest
				// of the server's life over a race we can simply outwait.
				log.warn("helper link: agent reports another live session; retrying with backoff");
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

	/** Diagnostics for the log line the panel shows. */
	public long droppedFrames() {
		return queue.droppedCount();
	}
}
