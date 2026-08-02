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

	private static final int M1_SECONDS = 60;
	private static final int M5_SECONDS = 300;
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
			//
			// The write loop is clamped to one window: past that every bucket has
			// already been zeroed, and re-zeroing them is work done on the tick
			// thread proportional to how long the server was already frozen —
			// precisely when it can least afford it.
			long gap = second - currentSecond;
			long from = gap > WINDOW_SECONDS ? second - WINDOW_SECONDS : currentSecond;
			for (long s = from; s < second; s++) {
				buckets[(int) Math.floorMod(s, WINDOW_SECONDS)] = (s == currentSecond) ? ticksThisSecond : 0L;
			}
			secondsElapsed += gap;
			currentSecond = second;
			ticksThisSecond = 0;
		}

		ticksThisSecond++;
	}

	/** The three trailing averages the protocol reports. */
	public record Rates(double m1, double m5, double m15) {
	}

	/**
	 * All three averages from one backward walk of the buckets.
	 *
	 * <p>The windows nest — one minute inside five inside fifteen — so the same
	 * running total answers all three at their boundaries. Calling {@link #tps}
	 * three times instead re-walks the array from scratch each time, doing 1,260
	 * bucket reads and as many {@code floorMod} divisions on the server thread per
	 * heartbeat where 900 reads and no divisions suffice.
	 */
	public Rates rates() {
		int window = (int) Math.min(WINDOW_SECONDS, secondsElapsed);
		if (window <= 0) {
			// Too early to have a meaningful average; report the nominal rate
			// rather than a misleading zero.
			return new Rates(20.0, 20.0, 20.0);
		}

		// Walk backwards with a plain decrementing index and a manual wrap, which
		// is the same arithmetic floorMod does without the per-step division.
		int index = (int) Math.floorMod(currentSecond - 1, WINDOW_SECONDS);
		long total = 0;
		long m1Total = 0;
		long m5Total = 0;
		for (int i = 1; i <= window; i++) {
			total += buckets[index];
			if (i == M1_SECONDS) {
				m1Total = total;
			}
			if (i == M5_SECONDS) {
				m5Total = total;
			}
			if (--index < 0) {
				index = WINDOW_SECONDS - 1;
			}
		}

		// A window longer than the uptime falls back to the whole observed span,
		// so a server up for 30s reports a real 30s average rather than one
		// diluted by 14.5 minutes of zeroes.
		return new Rates(
				average(window < M1_SECONDS ? total : m1Total, Math.min(window, M1_SECONDS)),
				average(window < M5_SECONDS ? total : m5Total, Math.min(window, M5_SECONDS)),
				average(total, window));
	}

	private static double average(long ticks, int seconds) {
		// A tick rate above nominal is an artefact of bucket boundaries, not a
		// server running fast.
		return Math.min((double) ticks / seconds, 20.0);
	}

	/**
	 * Average ticks per second over one trailing window.
	 *
	 * <p>Kept for tests and one-off queries; {@link #rates} is what the snapshot
	 * path uses.
	 *
	 * @param seconds window length; clamped to what has actually been observed
	 */
	public double tps(int seconds) {
		int window = (int) Math.min(Math.min(seconds, WINDOW_SECONDS), secondsElapsed);
		if (window <= 0) {
			return 20.0;
		}

		long total = 0;
		for (int i = 1; i <= window; i++) {
			total += buckets[(int) Math.floorMod(currentSecond - i, WINDOW_SECONDS)];
		}
		return average(total, window);
	}

	/** Whether enough time has passed for any average to be meaningful. */
	public boolean hasSamples() {
		return secondsElapsed > 0;
	}
}
