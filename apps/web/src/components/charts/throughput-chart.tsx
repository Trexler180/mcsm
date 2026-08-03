import { useId } from "react";
import { MAX_POINTS, formatRate } from "@/lib/upload-stats";
import { C_UPLOAD } from "./colors";

const W = 240;
const H = 48;

/**
 * Live throughput as a zero-anchored area chart.
 *
 * Newest sample sits at the right edge and older ones extend left over a fixed
 * time window, so points scroll past at a constant rate. Scaling x to the
 * sample count instead — as a plain sparkline does — would stretch the first
 * few readings across the whole width and then visibly compress them as the
 * buffer fills, which reads as the line sliding around rather than the
 * connection changing speed.
 *
 * All text lives outside the svg: preserveAspectRatio="none" is what lets the
 * plot fill any container width, and it would stretch glyphs with it.
 */
export function ThroughputChart({
  history,
  avgBps,
  label,
}: {
  /** Bytes per second per bucket, oldest first. */
  history: number[];
  /** Drawn as a recessive dashed reference line. */
  avgBps: number;
  /** Screen-reader summary; the visible numbers live alongside the chart. */
  label: string;
}) {
  const gradientId = useId();

  // Hold the height before the first buckets land so the panel doesn't jump.
  if (history.length < 2) {
    return <div style={{ height: H }} className="w-full" />;
  }

  // Zero-anchored: the filled area under a throughput line means "bytes moved",
  // so a non-zero baseline would overstate a slow connection. Headroom keeps the
  // peak off the top edge.
  const max = Math.max(...history, 1) * 1.15;
  const step = W / (MAX_POINTS - 1);
  const xAt = (i: number) => W - (history.length - 1 - i) * step;
  // Half a stroke of headroom top and bottom: a stalled connection plots at
  // zero, and without the inset the line's lower half is clipped off by the
  // viewBox edge exactly when it most needs to be readable. The fill still
  // closes on H, so the area itself reaches the baseline.
  const yAt = (v: number) => H - 1 - Math.min(v / max, 1) * (H - 2);

  const points = history.map((v, i) => `${xAt(i).toFixed(1)},${yAt(v).toFixed(1)}`);
  const area = `M${xAt(0).toFixed(1)},${H} L${points.join(" L")} L${W},${H} Z`;

  return (
    <svg
      width="100%"
      height={H}
      viewBox={`0 0 ${W} ${H}`}
      preserveAspectRatio="none"
      className="block"
      role="img"
      aria-label={label}
    >
      <defs>
        <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={C_UPLOAD} stopOpacity="0.35" />
          <stop offset="100%" stopColor={C_UPLOAD} stopOpacity="0" />
        </linearGradient>
      </defs>

      <path d={area} fill={`url(#${gradientId})`} />

      {avgBps > 0 && (
        <line
          // Only across the plotted region: before the window fills, a
          // reference line floating over empty space implies data that
          // isn't there yet.
          x1={xAt(0)}
          x2={W}
          y1={yAt(avgBps)}
          y2={yAt(avgBps)}
          stroke={C_UPLOAD}
          strokeWidth="1"
          strokeDasharray="3 3"
          strokeOpacity="0.4"
          vectorEffect="non-scaling-stroke"
        />
      )}

      <polyline
        points={points.join(" ")}
        fill="none"
        stroke={C_UPLOAD}
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
        // Without this the stroke is scaled by the same non-uniform transform
        // that stretches the plot to the container, so it thins out as the
        // dialog widens.
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}

/** The chart's own caption, kept next to it so the dashed line is never unlabeled. */
export function ThroughputLegend({ avgBps }: { avgBps: number }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <svg width="12" height="6" aria-hidden="true" className="flex-shrink-0">
        <line
          x1="0"
          x2="12"
          y1="3"
          y2="3"
          stroke={C_UPLOAD}
          strokeWidth="1"
          strokeDasharray="3 3"
          strokeOpacity="0.6"
        />
      </svg>
      avg {formatRate(avgBps)}
    </span>
  );
}
