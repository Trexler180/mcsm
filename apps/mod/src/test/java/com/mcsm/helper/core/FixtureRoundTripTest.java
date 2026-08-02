package com.mcsm.helper.core;

import com.google.gson.JsonElement;
import com.google.gson.JsonParser;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.stream.Stream;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;

/**
 * The Java half of the shared-fixture guard described in PROTOCOL.md §6.
 *
 * <p>The Go half has existed since the link shipped; this side did not, so wire
 * drift introduced in Java — a renamed field, a type that stopped matching the
 * naming policy, a payload class that lost a member — was uncaught by
 * construction. Both suites read the same files in {@code fixtures/}: one edit
 * there must be able to break both languages at once, which is impossible if
 * either side owns a copy.
 *
 * <p>The mechanism is the same on both sides. Parse a canonical frame into the
 * native types, re-serialise, and require semantic equality with the original
 * bytes. Gson silently ignores a wire field with no matching Java member, so
 * that field vanishes on re-serialisation and fails the comparison — which is
 * precisely the drift being guarded against.
 */
class FixtureRoundTripTest {

	@Test
	void everyFixtureSurvivesARoundTrip() throws IOException {
		List<Path> fixtures = fixtures();

		// A fixtures directory that quietly emptied would make this whole test
		// pass while checking nothing.
		assertFalse(fixtures.isEmpty(), "no fixtures found — the shared contract is missing");

		for (Path fixture : fixtures) {
			String raw = Files.readString(fixture);
			String name = fixture.getFileName().toString();

			Frames.Envelope env = Frames.decode(raw);
			assertNotNull(env, name + ": did not parse as an envelope");
			assertEquals(Frames.PROTOCOL_VERSION, env.v, name + ": unexpected protocol version");

			Object payload = decodePayload(env, name);
			String encoded = Frames.encode(env.type, env.id, env.ts, payload);

			assertSemanticallyEqual(name, raw, encoded);
		}
	}

	/** Converts a frame's data into its concrete type, mirroring the Go switch. */
	private static Object decodePayload(Frames.Envelope env, String name) {
		Object payload = switch (env.type) {
			case Messages.TYPE_HELLO -> Frames.payload(env, Messages.Hello.class);
			case Messages.TYPE_WELCOME -> Frames.payload(env, Messages.Welcome.class);
			case Messages.TYPE_SNAPSHOT -> Frames.payload(env, Messages.Snapshot.class);
			case Messages.TYPE_EVENT -> Frames.payload(env, Messages.Event.class);
			case Messages.TYPE_RPC_REQUEST -> Frames.payload(env, Messages.RpcRequest.class);
			case Messages.TYPE_RPC_RESPONSE -> Frames.payload(env, Messages.RpcResponse.class);
			default -> {
				fail(name + ": fixture uses unknown frame type " + env.type);
				yield null;
			}
		};
		assertNotNull(payload, name + ": payload did not decode");
		return payload;
	}

	/**
	 * Compares two frames ignoring key order and formatting, but not ignoring a
	 * missing or extra key. Gson's own equality compares numbers by value, so
	 * {@code 20.0} and {@code 20} match while {@code 20.0} and {@code 21} do not.
	 */
	private static void assertSemanticallyEqual(String name, String want, String got) {
		JsonElement expected = JsonParser.parseString(want);
		JsonElement actual = JsonParser.parseString(got);
		assertEquals(expected, actual, () -> name + ": round-trip changed the frame\n"
				+ "--- fixture ---\n" + expected + "\n--- after round-trip ---\n" + actual);
	}

	/**
	 * The boundary case that trips naive implementations, pinned on this side
	 * too: an empty list must stay {@code []} and must never become {@code null}.
	 *
	 * <p>The agent treats {@code players.list} as truth, so a null read as
	 * "unknown" rather than "nobody online" would leave players online forever in
	 * its derived state.
	 */
	@Test
	void emptyCollectionsStayEmptyRatherThanNull() throws IOException {
		String raw = Files.readString(fixturesDir().resolve("snapshot_empty_server.json"));

		Frames.Envelope env = Frames.decode(raw);
		assertNotNull(env);
		Messages.Snapshot snapshot = Frames.payload(env, Messages.Snapshot.class);
		assertNotNull(snapshot);

		assertNotNull(snapshot.dimensions, "dimensions decoded to null; expected an empty list");
		assertTrue(snapshot.dimensions.isEmpty(), "dimensions should be empty");
		assertNotNull(snapshot.players, "players decoded to null");
		assertNotNull(snapshot.players.list, "players.list decoded to null; expected an empty list");
		assertTrue(snapshot.players.list.isEmpty(), "players.list should be empty");

		String encoded = Frames.encode(Messages.TYPE_SNAPSHOT, env.ts, snapshot);
		JsonElement data = JsonParser.parseString(encoded).getAsJsonObject().get("data");
		assertTrue(data.getAsJsonObject().get("dimensions").isJsonArray(),
				"dimensions must re-encode as an array, not null");
		assertTrue(data.getAsJsonObject().getAsJsonObject("players").get("list").isJsonArray(),
				"players.list must re-encode as an array, not null");
	}

	/**
	 * The naming policy is what turns camelCase members into the snake_case the
	 * protocol specifies. It is configured once in {@link Frames}, so a change
	 * there would silently rename every field on the wire at once — this asserts
	 * the mapping directly rather than only through a whole-frame comparison.
	 */
	@Test
	void fieldNamesAreSnakeCaseOnTheWire() {
		Messages.Hello hello = new Messages.Hello();
		hello.modVersion = "1.0.1";
		hello.mcVersion = "26.2";
		hello.loader = "fabric";
		hello.loaderVersion = "0.19.3";
		hello.serverBrand = "fabric";
		hello.capabilities = List.of("vitals");

		JsonElement data = JsonParser.parseString(Frames.encode(Messages.TYPE_HELLO, 0L, hello))
				.getAsJsonObject()
				.get("data");

		for (String field : List.of("mod_version", "mc_version", "loader_version", "server_brand")) {
			assertTrue(data.getAsJsonObject().has(field), "expected wire field " + field);
		}
	}

	// ── Locating the shared fixtures ────────────────────────────────────────

	private static List<Path> fixtures() throws IOException {
		try (Stream<Path> entries = Files.list(fixturesDir())) {
			List<Path> found = new ArrayList<>(
					entries.filter(p -> p.getFileName().toString().endsWith(".json")).sorted().toList());
			return found;
		}
	}

	/**
	 * Finds {@code fixtures/} by walking up from the working directory, so the
	 * test runs the same whether Gradle, an IDE or a bare JUnit launcher decided
	 * where to start it.
	 */
	private static Path fixturesDir() {
		Path dir = Path.of("").toAbsolutePath();
		for (int depth = 0; dir != null && depth < 5; depth++, dir = dir.getParent()) {
			Path candidate = dir.resolve("fixtures");
			if (Files.isDirectory(candidate)) {
				return candidate;
			}
		}
		throw new IllegalStateException(
				"could not locate the shared fixtures directory from " + Path.of("").toAbsolutePath());
	}
}
