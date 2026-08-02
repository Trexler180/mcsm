package com.mcsm.helper.fabric;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * Pins the tick accounting, including the single-pass {@link TickStats#rates()}
 * used by the snapshot path.
 *
 * <p>{@code rates()} folds three nested windows into one walk of the bucket
 * array. That is only worth doing if it agrees with the straightforward
 * per-window {@link TickStats#tps(int)} in every case, so the equivalence is
 * asserted rather than assumed — including the awkward ones: before a full
 * window has elapsed, and after the ring has wrapped.
 */
class TickStatsTest {

	private static final long SECOND = 1_000_000_000L;

	/** Drives the clock forward, delivering {@code ticksPerSecond} ticks a second. */
	private static long run(TickStats stats, long fromNanos, int seconds, int ticksPerSecond) {
		long now = fromNanos;
		long step = SECOND / ticksPerSecond;
		for (int s = 0; s < seconds; s++) {
			for (int t = 0; t < ticksPerSecond; t++) {
				stats.onTick(now);
				now += step;
			}
		}
		return now;
	}

	@Test
	void reportsNominalRateBeforeAnySecondHasElapsed() {
		TickStats stats = new TickStats(0L);
		stats.onTick(0L);

		TickStats.Rates rates = stats.rates();
		assertEquals(20.0, rates.m1());
		assertEquals(20.0, rates.m5());
		assertEquals(20.0, rates.m15());
	}

	@Test
	void aHealthyServerReportsTwentyAcrossEveryWindow() {
		TickStats stats = new TickStats(0L);
		run(stats, 0L, 120, 20);

		TickStats.Rates rates = stats.rates();
		assertEquals(20.0, rates.m1(), 0.01);
		assertEquals(20.0, rates.m5(), 0.01);
		assertEquals(20.0, rates.m15(), 0.01);
	}

	@Test
	void aHalfSpeedServerReportsHalfTheRate() {
		TickStats stats = new TickStats(0L);
		run(stats, 0L, 120, 10);

		assertEquals(10.0, stats.rates().m1(), 0.2);
	}

	/**
	 * The short-uptime case: a window longer than the observed span must average
	 * over what actually happened, not dilute it with buckets that were never
	 * written.
	 */
	@Test
	void shortUptimeAveragesOverWhatWasObserved() {
		TickStats stats = new TickStats(0L);
		run(stats, 0L, 30, 20);

		TickStats.Rates rates = stats.rates();
		assertEquals(20.0, rates.m1(), 0.01);
		assertEquals(20.0, rates.m5(), 0.01);
		assertEquals(20.0, rates.m15(), 0.01);
	}

	@Test
	void ratesAgreeWithPerWindowTps() {
		TickStats stats = new TickStats(0L);
		// Uneven rates so the three windows genuinely differ from one another: a
		// slow stretch first, then recovery, leaves m1 above m15.
		long now = run(stats, 0L, 400, 8);
		run(stats, now, 100, 20);

		TickStats.Rates rates = stats.rates();
		assertEquals(stats.tps(60), rates.m1(), 1e-9, "m1 disagrees with tps(60)");
		assertEquals(stats.tps(300), rates.m5(), 1e-9, "m5 disagrees with tps(300)");
		assertEquals(stats.tps(900), rates.m15(), 1e-9, "m15 disagrees with tps(900)");
		assertTrue(rates.m1() > rates.m15(), "a recovering server should show m1 above m15");
	}

	@Test
	void ratesAgreeWithPerWindowTpsAfterTheRingWraps() {
		TickStats stats = new TickStats(0L);
		// Well past the 900-bucket window, so every index has been reused.
		long now = run(stats, 0L, 1000, 20);
		run(stats, now, 200, 12);

		TickStats.Rates rates = stats.rates();
		assertEquals(stats.tps(60), rates.m1(), 1e-9);
		assertEquals(stats.tps(300), rates.m5(), 1e-9);
		assertEquals(stats.tps(900), rates.m15(), 1e-9);
	}

	/**
	 * A stall must show up as lost ticks rather than as a gap that quietly
	 * disappears from the average.
	 */
	@Test
	void aLongStallShowsAsLostTicks() {
		TickStats stats = new TickStats(0L);
		long now = run(stats, 0L, 120, 20);

		// Frozen for an hour: four times the whole window.
		stats.onTick(now + 3600 * SECOND);

		assertEquals(0.0, stats.rates().m1(), 0.01, "a frozen hour must read as zero TPS");
		assertEquals(0.0, stats.rates().m15(), 0.01);
	}

	/**
	 * The catch-up after a gap must cost what the window costs, not what the gap
	 * costs, because it runs on the tick thread of a server that has just proven
	 * it has no headroom.
	 *
	 * <p>The gap here is absurd on purpose. A realistic stall — even an hour —
	 * fills so few buckets either way that no timing assertion could tell the
	 * clamped loop from the unclamped one, which would make this test decorative.
	 * At a century the unclamped loop is billions of iterations and the clamped
	 * one is 900, so completing at all is the assertion. Nothing about the
	 * clamp's correctness depends on the gap being plausible: past one window
	 * every bucket has already been zeroed, so the extra passes were rewriting
	 * zeroes over zeroes.
	 */
	@Test
	void catchUpIsBoundedByTheWindowNotTheGap() {
		TickStats stats = new TickStats(0L);
		long now = run(stats, 0L, 120, 20);

		long century = 100L * 365 * 24 * 3600 * SECOND;
		long start = System.nanoTime();
		stats.onTick(now + century);
		long elapsedNanos = System.nanoTime() - start;

		assertEquals(0.0, stats.rates().m15(), 0.01, "a century of silence must read as zero TPS");
		assertTrue(elapsedNanos < 2_000_000_000L,
				"catch-up took " + elapsedNanos + "ns, which means it scaled with the gap");
	}

	@Test
	void aBriefStallOnlyZeroesTheSecondsItCovered() {
		TickStats stats = new TickStats(0L);
		long now = run(stats, 0L, 120, 20);

		// Five seconds with no ticks at all, then back to full speed.
		long afterStall = now + 5 * SECOND;
		run(stats, afterStall, 55, 20);

		// 55 full seconds plus 5 empty ones inside the one-minute window.
		assertEquals(20.0 * 55 / 60, stats.rates().m1(), 0.5);
	}
}
