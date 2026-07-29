package com.mcsm.helper.fabric;

import com.mcsm.helper.core.LinkClient;
import com.mcsm.helper.core.LinkConfig;
import com.mcsm.helper.core.Messages;
import com.mcsm.helper.core.ServerFacade;
import com.mojang.brigadier.CommandDispatcher;
import com.mojang.brigadier.ParseResults;
import com.mojang.brigadier.exceptions.CommandSyntaxException;
import net.fabricmc.fabric.api.entity.event.v1.ServerLivingEntityEvents;
import net.fabricmc.fabric.api.event.lifecycle.v1.ServerLifecycleEvents;
import net.fabricmc.fabric.api.event.lifecycle.v1.ServerTickEvents;
import net.fabricmc.fabric.api.networking.v1.ServerPlayConnectionEvents;
import net.fabricmc.loader.api.FabricLoader;
import net.minecraft.commands.CommandSource;
import net.minecraft.commands.CommandSourceStack;
import net.minecraft.network.chat.Component;
import net.minecraft.server.MinecraftServer;
import net.minecraft.server.level.ServerLevel;
import net.minecraft.server.level.ServerPlayer;
import net.minecraft.server.players.NameAndId;
import net.minecraft.server.players.PlayerList;
import net.minecraft.world.entity.Entity;
import net.minecraft.world.entity.LivingEntity;
import org.slf4j.Logger;

import java.lang.management.GarbageCollectorMXBean;
import java.lang.management.ManagementFactory;
import java.lang.management.MemoryUsage;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Locale;
import java.util.function.Consumer;

/**
 * Binds the platform-agnostic core to a Fabric dedicated server.
 *
 * <p>This is the only class that imports Minecraft. Everything protocol-shaped
 * lives in {@code com.mcsm.helper.core} and knows nothing about Fabric, so a
 * Paper or NeoForge port means writing a sibling of this file rather than
 * touching the link, queueing, auth or dispatch code.
 *
 * <p>Contains no mixins: every hook here is a public Fabric API event.
 */
public final class FabricAdapter implements ServerFacade {

	private static final int MB = 1024 * 1024;

	private final MinecraftServer server;
	private final String modVersion;
	private final Logger log;
	private final TickStats tickStats = new TickStats();
	private final long startedAtMillis = System.currentTimeMillis();

	private FabricAdapter(MinecraftServer server, String modVersion, Logger log) {
		this.server = server;
		this.modVersion = modVersion;
		this.log = log;
	}

	/**
	 * Registers lifecycle hooks. The link itself is not started until the server
	 * is fully up, so the agent never sees a half-initialised server.
	 */
	public static void install(LinkConfig config, FabricLoader loader, Logger log, Consumer<LinkClient> onStarted) {
		String modVersion = loader.getModContainer("mcsm-helper")
				.map(c -> c.getMetadata().getVersion().getFriendlyString())
				.orElse("unknown");

		final LinkClient[] clientRef = new LinkClient[1];
		final FabricAdapter[] adapterRef = new FabricAdapter[1];

		ServerLifecycleEvents.SERVER_STARTED.register(server -> {
			FabricAdapter adapter = new FabricAdapter(server, modVersion, log);
			LinkClient client = new LinkClient(config, adapter, new Slf4jLogger(log));

			adapterRef[0] = adapter;
			clientRef[0] = client;
			onStarted.accept(client);

			client.start();
			client.enqueueEvent(Messages.Event.simple(Messages.EVENT_SERVER_READY));
		});

		ServerLifecycleEvents.SERVER_STOPPING.register(server -> {
			LinkClient client = clientRef[0];
			if (client != null) {
				client.enqueueEvent(Messages.Event.simple(Messages.EVENT_SERVER_STOPPING));
				client.stop();
			}
		});

		// Tick accounting must be registered regardless of link state so the
		// first snapshot after connecting already has real averages behind it.
		ServerTickEvents.END_SERVER_TICK.register(server -> {
			FabricAdapter adapter = adapterRef[0];
			if (adapter != null) {
				adapter.tickStats.onTick();
			}
		});

		ServerPlayConnectionEvents.JOIN.register((handler, sender, server) -> {
			LinkClient client = clientRef[0];
			if (client != null) {
				ServerPlayer player = handler.player;
				client.enqueueEvent(Messages.Event.player(
						Messages.EVENT_PLAYER_JOIN, player.getUUID().toString(), player.getName().getString()));
			}
		});

		ServerPlayConnectionEvents.DISCONNECT.register((handler, server) -> {
			LinkClient client = clientRef[0];
			if (client != null) {
				ServerPlayer player = handler.player;
				client.enqueueEvent(Messages.Event.player(
						Messages.EVENT_PLAYER_LEAVE, player.getUUID().toString(), player.getName().getString()));
			}
		});

		ServerLivingEntityEvents.AFTER_DEATH.register((entity, damageSource) -> {
			LinkClient client = clientRef[0];
			if (client == null || !(entity instanceof ServerPlayer player)) {
				return;
			}
			Messages.Event event = Messages.Event.player(
					Messages.EVENT_PLAYER_DEATH, player.getUUID().toString(), player.getName().getString());
			event.message = safeDeathMessage(player, damageSource);
			client.enqueueEvent(event);
		});
	}

	private static String safeDeathMessage(ServerPlayer player, Object damageSource) {
		try {
			return player.getCombatTracker().getDeathMessage().getString();
		} catch (Throwable t) {
			// A death message is nice-to-have; never let formatting it break the
			// death event itself.
			return "";
		}
	}

	// ── Identity ────────────────────────────────────────────────────────────

	@Override
	public String modVersion() {
		return modVersion;
	}

	@Override
	public String mcVersion() {
		return server.getServerVersion();
	}

	@Override
	public String loader() {
		return "fabric";
	}

	@Override
	public String loaderVersion() {
		return FabricLoader.getInstance().getModContainer("fabricloader")
				.map(c -> c.getMetadata().getVersion().getFriendlyString())
				.orElse("unknown");
	}

	@Override
	public String serverBrand() {
		return server.getServerModName();
	}

	@Override
	public void submit(Runnable task) {
		// MinecraftServer is an event loop; execute() enqueues onto the server
		// thread and returns immediately.
		server.execute(task);
	}

	// ── Sampling ────────────────────────────────────────────────────────────

	@Override
	public Messages.Snapshot sampleSnapshot() {
		Messages.Snapshot snapshot = new Messages.Snapshot();
		snapshot.uptimeMs = System.currentTimeMillis() - startedAtMillis;
		snapshot.tps = sampleTps();
		snapshot.mspt = sampleMspt();
		snapshot.heap = sampleHeap();
		snapshot.gc = sampleGc();

		long totalChunks = 0;
		long totalEntities = 0;
		List<Messages.Dimension> dimensions = new ArrayList<>();

		for (ServerLevel level : server.getAllLevels()) {
			long chunks = level.getChunkSource().getLoadedChunksCount();
			long entities = countEntities(level);

			Messages.Dimension dimension = new Messages.Dimension();
			dimension.id = level.dimension().identifier().toString();
			dimension.chunks = chunks;
			dimension.entities = entities;
			dimensions.add(dimension);

			totalChunks += chunks;
			totalEntities += entities;
		}

		snapshot.dimensions = dimensions;

		Messages.Chunks chunks = new Messages.Chunks();
		chunks.loaded = totalChunks;
		snapshot.chunks = chunks;

		Messages.Entities entities = new Messages.Entities();
		entities.total = totalEntities;
		snapshot.entities = entities;

		snapshot.players = samplePlayers();
		return snapshot;
	}

	/**
	 * Counts entities in a level.
	 *
	 * <p>This is the one genuinely O(entities) step in the snapshot path. There
	 * is no public counter to read instead, so it is kept to a plain iteration
	 * with no allocation and runs only once per heartbeat (15s by default), not
	 * per tick.
	 */
	private static long countEntities(ServerLevel level) {
		long count = 0;
		for (Entity ignored : level.getAllEntities()) {
			count++;
		}
		return count;
	}

	private Messages.Tps sampleTps() {
		Messages.Tps tps = new Messages.Tps();
		tps.m1 = round2(tickStats.tps(60));
		tps.m5 = round2(tickStats.tps(300));
		tps.m15 = round2(tickStats.tps(900));
		return tps;
	}

	private Messages.Mspt sampleMspt() {
		Messages.Mspt mspt = new Messages.Mspt();

		long[] samples = server.getTickTimesNanos();
		if (samples == null || samples.length == 0) {
			return mspt;
		}

		// Copy before sorting: the array is the server's live ring buffer.
		long[] sorted = Arrays.copyOf(samples, samples.length);
		Arrays.sort(sorted);

		// Zero entries are unfilled ring slots on a freshly started server, not
		// ticks that took no time; including them would understate MSPT.
		int firstUsed = 0;
		while (firstUsed < sorted.length && sorted[firstUsed] == 0L) {
			firstUsed++;
		}
		if (firstUsed == sorted.length) {
			return mspt;
		}

		long[] used = Arrays.copyOfRange(sorted, firstUsed, sorted.length);

		long total = 0;
		for (long value : used) {
			total += value;
		}

		mspt.avg = round2(nanosToMillis((double) total / used.length));
		mspt.p50 = round2(nanosToMillis(percentile(used, 0.50)));
		mspt.p95 = round2(nanosToMillis(percentile(used, 0.95)));
		mspt.p99 = round2(nanosToMillis(percentile(used, 0.99)));
		mspt.max = round2(nanosToMillis(used[used.length - 1]));
		return mspt;
	}

	private static double percentile(long[] sorted, double fraction) {
		if (sorted.length == 0) {
			return 0;
		}
		int index = (int) Math.ceil(fraction * sorted.length) - 1;
		return sorted[Math.max(0, Math.min(index, sorted.length - 1))];
	}

	private static Messages.Heap sampleHeap() {
		MemoryUsage usage = ManagementFactory.getMemoryMXBean().getHeapMemoryUsage();
		Messages.Heap heap = new Messages.Heap();
		heap.usedMb = usage.getUsed() / MB;
		heap.committedMb = usage.getCommitted() / MB;
		// -1 means unbounded; report 0 rather than a nonsensical negative.
		heap.maxMb = usage.getMax() > 0 ? usage.getMax() / MB : 0;
		return heap;
	}

	private static Messages.Gc sampleGc() {
		Messages.Gc gc = new Messages.Gc();
		for (GarbageCollectorMXBean bean : ManagementFactory.getGarbageCollectorMXBeans()) {
			long count = bean.getCollectionCount();
			long time = bean.getCollectionTime();
			if (count > 0) {
				gc.collections += count;
			}
			if (time > 0) {
				gc.timeMs += time;
			}
		}
		return gc;
	}

	private Messages.Players samplePlayers() {
		PlayerList list = server.getPlayerList();
		Messages.Players players = new Messages.Players();
		players.online = list.getPlayerCount();
		players.max = list.getMaxPlayers();
		players.list = new ArrayList<>();

		for (ServerPlayer player : list.getPlayers()) {
			Messages.PlayerInfo info = new Messages.PlayerInfo();
			info.uuid = player.getUUID().toString();
			info.name = player.getName().getString();
			info.pingMs = player.connection.latency();
			info.dimension = player.level().dimension().identifier().toString();
			players.list.add(info);
		}
		return players;
	}

	// ── RPC operations ──────────────────────────────────────────────────────

	@Override
	public boolean kick(String name, String reason) {
		ServerPlayer player = server.getPlayerList().getPlayerByName(name);
		if (player == null) {
			return false;
		}
		String text = (reason == null || reason.isEmpty()) ? "Kicked by an operator" : reason;
		player.connection.disconnect(Component.literal(text));
		return true;
	}

	@Override
	public boolean ban(String name, String reason) {
		// Routed through the vanilla command rather than constructing a ban
		// entry directly: the command handles offline players, profile lookup
		// and persistence, and going around it is how bans end up inconsistent
		// with banned-players.json.
		String command = (reason == null || reason.isEmpty())
				? "ban " + name
				: "ban " + name + " " + reason;
		return exec(command).success();
	}

	@Override
	public boolean pardon(String name) {
		return exec("pardon " + name).success();
	}

	@Override
	public boolean whitelistAdd(String name) {
		return exec("whitelist add " + name).success();
	}

	@Override
	public boolean whitelistRemove(String name) {
		return exec("whitelist remove " + name).success();
	}

	@Override
	public List<String> whitelistList() {
		return new ArrayList<>(Arrays.asList(server.getPlayerList().getWhiteListNames()));
	}

	@Override
	public boolean op(String name) {
		NameAndId target = resolve(name);
		if (target == null) {
			return false;
		}
		server.getPlayerList().op(target);
		return true;
	}

	@Override
	public boolean deop(String name) {
		NameAndId target = resolve(name);
		if (target == null) {
			return false;
		}
		server.getPlayerList().deop(target);
		return true;
	}

	/**
	 * Resolves a name to an identity.
	 *
	 * <p>Only online players resolve reliably here: an offline lookup would need
	 * a profile cache round trip that can block. Op/deop of an offline player is
	 * therefore left to the command path.
	 */
	private NameAndId resolve(String name) {
		ServerPlayer player = server.getPlayerList().getPlayerByName(name);
		if (player == null) {
			return null;
		}
		return new NameAndId(player.getUUID(), player.getName().getString());
	}

	@Override
	public void broadcast(String message) {
		server.getPlayerList().broadcastSystemMessage(Component.literal(message), false);
	}

	@Override
	public boolean messagePlayer(String name, String message) {
		ServerPlayer player = server.getPlayerList().getPlayerByName(name);
		if (player == null) {
			return false;
		}
		player.sendSystemMessage(Component.literal(message));
		return true;
	}

	@Override
	public void save(boolean flush) {
		server.saveEverything(true, flush, false);
	}

	@Override
	public void stop() {
		server.halt(false);
	}

	@Override
	public ExecResult exec(String command) {
		List<String> output = new ArrayList<>();

		CommandSource capture = new CommandSource() {
			@Override
			public void sendSystemMessage(Component message) {
				output.add(message.getString());
			}

			@Override
			public boolean acceptsSuccess() {
				return true;
			}

			@Override
			public boolean acceptsFailure() {
				return true;
			}

			@Override
			public boolean shouldInformAdmins() {
				return false;
			}
		};

		CommandSourceStack stack = server.createCommandSourceStack().withSource(capture);
		CommandDispatcher<CommandSourceStack> dispatcher = server.getCommands().getDispatcher();

		try {
			// Using the dispatcher rather than performPrefixedCommand is what
			// makes real success/failure available: execute() returns a result
			// and throws on a rejected command, whereas the convenience method
			// swallows both.
			ParseResults<CommandSourceStack> parsed = dispatcher.parse(stripLeadingSlash(command), stack);
			int result = dispatcher.execute(parsed);
			return new ExecResult(output, result > 0);
		} catch (CommandSyntaxException e) {
			output.add(e.getMessage());
			return new ExecResult(output, false);
		} catch (Throwable t) {
			log.debug("MCSM helper: command '{}' failed", command, t);
			output.add(String.valueOf(t.getMessage()));
			return new ExecResult(output, false);
		}
	}

	private static String stripLeadingSlash(String command) {
		String trimmed = command.trim();
		return trimmed.startsWith("/") ? trimmed.substring(1) : trimmed;
	}

	private static double nanosToMillis(double nanos) {
		return nanos / 1_000_000.0d;
	}

	private static double round2(double value) {
		return Math.round(value * 100.0d) / 100.0d;
	}

	@SuppressWarnings("unused")
	private static String lower(String value) {
		return value == null ? "" : value.toLowerCase(Locale.ROOT);
	}

	/** Adapts SLF4J to the core's tiny logging seam. */
	private record Slf4jLogger(Logger delegate) implements LinkClient.Logger {
		@Override
		public void info(String message) {
			delegate.info(message);
		}

		@Override
		public void warn(String message) {
			delegate.warn(message);
		}

		@Override
		public void debug(String message) {
			delegate.debug(message);
		}
	}
}
