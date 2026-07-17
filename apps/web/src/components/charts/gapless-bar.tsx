// Recharts positions bars at fractional x/width, so even at barCategoryGap={0}
// the antialiased edges leave hairline seams between neighbours. Snap each bar's
// left edge down and right edge up to whole pixels, then overlap the right edge
// one more pixel so each bar paints over any residual seam with its neighbour —
// a true gapless histogram.
//
// Each top corner takes one of three shapes depending on the neighbour on that
// side, so the filled silhouette flows smoothly instead of stepping in blocks:
//   - "round": the neighbour is shorter, so the corner is exposed → round it
//     outward (a small convex cap on the top of the bar).
//   - "rise":  the neighbour is taller → curl the top up by the same small
//     radius, so the corner lifts toward the taller neighbour instead of meeting
//     it at a hard right angle. The amount is fixed — the neighbour's actual
//     height doesn't matter, it's just the mirror of the round-down curl.
//   - "square": the neighbour is the same height → leave it flat so equal bars
//     read as one continuous top.
const BAR_RADIUS = 8;

type CornerMode = "round" | "rise" | "square";

function cornerMode(neighbour: number, self: number): CornerMode {
  if (neighbour < self) return "round";
  if (neighbour > self) return "rise";
  return "square";
}

type BarShapeProps = {
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  fill?: string;
  index?: number;
  values?: number[];
};

export function GaplessBar({
  x = 0,
  y = 0,
  width = 0,
  height = 0,
  fill,
  index = 0,
  values = [],
}: BarShapeProps) {
  if (height <= 0) return null;

  const self = values[index] ?? 0;
  const leftMode = cornerMode(values[index - 1] ?? 0, self);
  const rightMode = cornerMode(values[index + 1] ?? 0, self);

  const left = Math.floor(x);
  const right = Math.ceil(x + width) + 1;
  const w = right - left;
  const top = y;
  const bottom = y + height;

  const r = Math.max(0, Math.min(BAR_RADIUS, height, w / 2));

  // Every corner uses the same radius r; only the direction of the vertical
  // curl differs: "round" dips the edge below the top (convex cap over an
  // exposed corner), "rise" lifts it the same amount above the top (curling up
  // toward a taller neighbour), "square" keeps it level.
  const sideGeom = (mode: CornerMode) => {
    if (mode === "round") return { reach: r, edgeY: top + r };
    if (mode === "rise") return { reach: r, edgeY: top - r };
    return { reach: 0, edgeY: top };
  };
  const L = sideGeom(leftMode);
  const R = sideGeom(rightMode);

  // Each arc pivots on the actual corner point (left/right, top) as its Bézier
  // control, so the tangents are horizontal along the flat top and vertical
  // along the edge — a clean quarter turn whether the corner rounds over or
  // sweeps up.
  const parts = [`M ${left} ${L.edgeY}`];
  if (leftMode !== "square") parts.push(`Q ${left} ${top} ${left + L.reach} ${top}`);
  parts.push(`L ${right - R.reach} ${top}`);
  if (rightMode !== "square") parts.push(`Q ${right} ${top} ${right} ${R.edgeY}`);
  else parts.push(`L ${right} ${top}`);
  parts.push(`L ${right} ${bottom}`, `L ${left} ${bottom}`, "Z");

  return <path d={parts.join(" ")} fill={fill} />;
}
