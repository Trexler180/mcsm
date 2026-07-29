package com.mcsm.helper;

import com.mcsm.helper.core.LinkClient;
import com.mcsm.helper.core.LinkConfig;
import com.mcsm.helper.fabric.FabricAdapter;
import net.fabricmc.api.ModInitializer;
import net.fabricmc.loader.api.FabricLoader;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.Optional;

/**
 * Fabric entrypoint.
 *
 * <p>Deliberately inert unless the agent launched this server: with no
 * {@code MCSM_*} environment the mod loads, logs one debug line, and does
 * nothing else. A server started outside the panel must behave exactly as if
 * this jar were not present.
 */
public final class McsmHelper implements ModInitializer {

	public static final String MOD_ID = "mcsm-helper";

	private static final Logger LOGGER = LoggerFactory.getLogger(MOD_ID);

	private static volatile LinkClient client;

	@Override
	public void onInitialize() {
		Optional<LinkConfig> config = LinkConfig.fromEnvironment();
		if (config.isEmpty()) {
			LOGGER.debug("MCSM helper: no agent environment present, staying idle");
			return;
		}

		LOGGER.info("MCSM helper: linking to agent for server {}", config.get().serverId());
		FabricAdapter.install(config.get(), FabricLoader.getInstance(), LOGGER, c -> client = c);
	}

	/** The active link, if any. Exposed for diagnostics. */
	public static LinkClient link() {
		return client;
	}
}
