package com.mcsm.helper.fabric;

/**
 * Rolling tick-rate accounting.
 *
 * <p>Vanilla exposes recent tick <em>durations</em> ({@code getTickTimesNanos})
 * but no 1/5/15-minute tick-rate averages — those are a Paper extension. Rather
 * than fake them from instantaneous MSPT, this counts ticks into one-second
 * buckets and averages over the requested window, which is what an operator
 * means by "TPS over the last 5 minutes".
 *
 * <p>Memory is a fixed 900 longs (15 minutes of one-second buckets), and the
 * per-tick cost is an increment plus an occasional bucket rotation.
 *
 * <p>Not thread-safe by design: {@link #onTick} is only ever called from the
 * server thread. {@link #tps} reads without locking and may observe a bucket
 * mid-rotation, which can only skew a single second of a 60+ second average.
 */
public final class TickStats {

	private static final int WINDOW_SECONDS = 900; // 15 minutes

	private final long[] buckets = new long[WINDOW_SECONDS];
	private final long startNanos;

	private long currentSecond = -1;
	private long ticksThisSecond;
	private long secondsElapsed;

	public TickStats() {
		this(System.nanoTime());
	}

	/** Visible for testing. */
	public TickStats(long startNanos) {
		this.startNanos = startNanos;
	}

	/** Called once per server tick, on the server thread. */
	public void onTick() {
		onTick(System.nanoTime());
	}

	/** Visible for testing. */
	public void onTick(long nowNanos) {
		long second = (nowNanos - startNanos) / 1_000_000_000L;

		if (currentSecond < 0) {
			currentSecond = second;
		}

		if (second != currentSecond) {
			// Close out the elapsed second, and zero any seconds we skipped
			// entirely — a stalled server must show as lost ticks, not as a gap
			// that quietly disappears from the average.
			for (long s = currentSecond; s < second; s++) {
				buckets[(int) Math.floorMod(s, WINDOW_SECONDS)] = (s == currentSecond) ? ticksThisSecond : 0L;
				secondsElapsed++;
			}
			currentSecond = second;
			ticksThisSecond = 0;
		}

		ticksThisSecond++;
	}

	/**
	 * Average ticks per second over the trailing window.
	 *
	 * @param seconds window length; clamped to what has actually been observed,
	 *                so a server up for 30s reports a real 30s average rather
	 *                than one diluted by 14.5 minutes of zeroes
	 */
	public double tps(int seconds) {
		int window = (int) Math.min(Math.min(seconds, WINDOW_SECONDS), secondsElapsed);
		if (window <= 0) {
			// Too early to have a meaningful average; report the nominal rate
			// rather than a misleading zero.
			return 20.0;
		}

		long total = 0;
		for (int i = 1; i <= window; i++) {
			total += buckets[(int) Math.floorMod(currentSecond - i, WINDOW_SECONDS)];
		}
		double rate = (double) total / window;
		// A tick rate above nominal is an artefact of bucket boundaries, not a
		// server running fast.
		return Math.min(rate, 20.0);
	}

	/** Whether enough time has passed for any average to be meaningful. */
	public boolean hasSamples() {
		return secondsElapsed > 0;
	}
}
