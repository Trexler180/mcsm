package com.mcsm.helper.core;

import java.util.ArrayDeque;
import java.util.concurrent.atomic.AtomicLong;

/**
 * Bounded outbound frame queue implementing the backpressure policy in
 * PROTOCOL.md §5.
 *
 * <p>The contract that matters: {@link #offer} never blocks and never throws, so
 * it is safe to call from the server tick thread. When the queue is full,
 * droppable frames (events) are discarded oldest-first to make room; frames that
 * carry correctness — snapshots and RPC responses — are never sacrificed for an
 * event.
 */
public final class OutboundQueue {

	/** A frame plus whether it may be dropped under pressure. */
	public record Item(String payload, boolean droppable) {
	}

	private final ArrayDeque<Item> queue;
	private final int capacity;
	private final AtomicLong dropped = new AtomicLong();

	public OutboundQueue(int capacity) {
		if (capacity < 1) {
			throw new IllegalArgumentException("capacity must be positive");
		}
		this.capacity = capacity;
		this.queue = new ArrayDeque<>(capacity);
	}

	/**
	 * Enqueues a frame. Never blocks.
	 *
	 * @return true if the frame was accepted, false if it had to be dropped.
	 */
	public boolean offer(String payload, boolean droppable) {
		synchronized (queue) {
			if (queue.size() >= capacity && !evictOneDroppable()) {
				// Nothing droppable to evict: the queue is full of frames that
				// carry correctness. Sacrifice the incoming frame instead — but
				// only if it is itself droppable, otherwise drop the oldest
				// non-droppable frame, which is the stalest and least useful.
				if (droppable) {
					dropped.incrementAndGet();
					return false;
				}
				queue.pollFirst();
				dropped.incrementAndGet();
			}
			queue.addLast(new Item(payload, droppable));
			queue.notifyAll();
			return true;
		}
	}

	/** Removes the oldest droppable frame. Caller must hold the lock. */
	private boolean evictOneDroppable() {
		var it = queue.iterator();
		while (it.hasNext()) {
			if (it.next().droppable()) {
				it.remove();
				dropped.incrementAndGet();
				return true;
			}
		}
		return false;
	}

	/**
	 * Blocks the calling thread until a frame is available or the wait elapses.
	 * Only ever called from the dedicated IO thread — never from a game thread.
	 *
	 * @return the next frame, or null if none arrived within the timeout.
	 */
	public Item take(long timeoutMillis) throws InterruptedException {
		long deadline = System.nanoTime() + timeoutMillis * 1_000_000L;
		synchronized (queue) {
			while (queue.isEmpty()) {
				long remaining = deadline - System.nanoTime();
				if (remaining <= 0) {
					return null;
				}
				queue.wait(Math.max(1, remaining / 1_000_000L));
			}
			return queue.pollFirst();
		}
	}

	/** Discards everything pending. Used when a session ends. */
	public void clear() {
		synchronized (queue) {
			queue.clear();
		}
	}

	public int size() {
		synchronized (queue) {
			return queue.size();
		}
	}

	/** Total frames dropped since startup — surfaced in logs for diagnosis. */
	public long droppedCount() {
		return dropped.get();
	}
}
