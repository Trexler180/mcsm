import type { CSSProperties } from "react";
import { AlertTriangle, Gauge, MemoryStick } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import type { Node } from "@/lib/types";

const MIN_MEMORY_MB = 512;
const MEMORY_STEP_MB = 256;

interface MemoryAllocationProps {
  node?: Node;
  value: string;
  onChange: (value: string) => void;
  minimumValue?: string;
  onMinimumChange?: (value: string) => void;
}

function numericMemory(value: string): number | null {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : null;
}

export function formatMemory(mb: number): string {
  if (mb >= 1024) {
    const gb = mb / 1024;
    return `${gb >= 10 || Number.isInteger(gb) ? gb.toFixed(0) : gb.toFixed(1)} GB`;
  }
  return `${Math.round(mb)} MB`;
}

export function isMemoryAllocationValid(
  maximum: string,
  minimum = String(MIN_MEMORY_MB),
  totalMb?: number | null,
): boolean {
  const max = numericMemory(maximum);
  const min = numericMemory(minimum);
  return (
    max !== null &&
    min !== null &&
    min <= max &&
    max >= MIN_MEMORY_MB &&
    (totalMb == null || max <= totalMb)
  );
}

export function MemoryAllocation({
  node,
  value,
  onChange,
  minimumValue,
  onMinimumChange,
}: MemoryAllocationProps) {
  const selectedMb = numericMemory(value);
  const minimumMb = minimumValue ? numericMemory(minimumValue) : null;
  const totalMb = node?.memory_mb ?? null;
  const usedMb =
    totalMb == null || node?.mem_used_mb == null
      ? null
      : Math.min(totalMb, Math.max(0, node.mem_used_mb));
  const freeMb = totalMb == null || usedMb == null ? null : totalMb - usedMb;
  // Prefer the live free amount. A node can still have a configured/detected
  // physical total before its first usage heartbeat, so keep the control useful
  // in that state and label the fallback honestly as total capacity.
  const rangeMb = freeMb ?? totalMb;
  const sliderCeiling =
    rangeMb == null
      ? null
      : Math.floor(rangeMb / MEMORY_STEP_MB) * MEMORY_STEP_MB;
  const sliderEnabled = sliderCeiling !== null && sliderCeiling >= MIN_MEMORY_MB;
  const sliderValue = sliderEnabled
    ? Math.min(
        sliderCeiling,
        Math.max(MIN_MEMORY_MB, selectedMb ?? MIN_MEMORY_MB),
      )
    : MIN_MEMORY_MB;
  const sliderPercent =
    sliderEnabled && sliderCeiling !== MIN_MEMORY_MB
      ? ((sliderValue - MIN_MEMORY_MB) /
          (sliderCeiling! - MIN_MEMORY_MB)) *
        100
      : 0;
  const usedPercent =
    totalMb && usedMb != null ? Math.min(100, (usedMb / totalMb) * 100) : 0;
  const exceedsFree = selectedMb != null && freeMb != null && selectedMb > freeMb;
  const exceedsTotal = selectedMb != null && totalMb != null && selectedMb > totalMb;
  const minimumExceedsMaximum =
    selectedMb != null && minimumMb != null && minimumMb > selectedMb;
  const remainingMb =
    selectedMb != null && freeMb != null ? freeMb - selectedMb : null;

  const setMaximum = (nextMb: number) => {
    onChange(String(nextMb));
    if (
      minimumValue !== undefined &&
      onMinimumChange &&
      numericMemory(minimumValue) !== null &&
      Number(minimumValue) > nextMb
    ) {
      onMinimumChange(String(nextMb));
    }
  };

  return (
    <div className="overflow-hidden rounded-lg border border-border bg-surface-2/45">
      <div className="flex flex-col gap-3 p-3.5 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <div className="grid h-7 w-7 flex-shrink-0 place-items-center rounded-md bg-accent/10 text-accent">
              <MemoryStick className="h-4 w-4" />
            </div>
            <div>
              <Label htmlFor="maximum-memory" className="text-sm text-text-primary">
                Maximum RAM
              </Label>
              <p className="text-xs text-text-secondary">
                The most memory this server can use.
              </p>
            </div>
          </div>
        </div>

        <div className="relative w-full sm:w-32">
          <Input
            id="maximum-memory"
            aria-label="Maximum RAM in MB"
            type="number"
            min={MIN_MEMORY_MB}
            step={MEMORY_STEP_MB}
            max={totalMb ?? undefined}
            value={value}
            onChange={(event) => onChange(event.target.value)}
            className="pr-10 text-right font-mono text-base font-semibold"
          />
          <span className="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-[11px] text-text-secondary">
            MB
          </span>
        </div>
      </div>

      <div className="space-y-3 border-t border-border px-3.5 py-3">
        {totalMb != null && usedMb != null && freeMb != null ? (
          <div className="space-y-1.5" aria-label="Host memory usage">
            <div className="flex items-center justify-between gap-3 text-xs">
              <span className="flex items-center gap-1.5 font-medium text-text-primary">
                <Gauge className="h-3.5 w-3.5 text-text-secondary" />
                {formatMemory(freeMb)} free
              </span>
              <span className="text-text-secondary">
                {formatMemory(usedMb)} used · {formatMemory(totalMb)} total
              </span>
            </div>
            <div className="h-2 overflow-hidden rounded-full bg-background" title={`${formatMemory(freeMb)} free of ${formatMemory(totalMb)}`}>
              <div
                className="h-full rounded-full bg-text-secondary/50 transition-[width]"
                style={{ width: `${usedPercent}%` }}
              />
            </div>
          </div>
        ) : totalMb != null ? (
          <div className="flex items-center justify-between gap-3 rounded-md border border-dashed border-border px-2.5 py-2 text-xs">
            <span className="flex items-center gap-1.5 text-text-primary">
              <Gauge className="h-3.5 w-3.5 text-text-secondary" />
              {formatMemory(totalMb)} total
            </span>
            <span className="text-text-secondary">Live usage unavailable</span>
          </div>
        ) : (
          <div className="flex items-center gap-2 rounded-md border border-dashed border-border px-2.5 py-2 text-xs text-text-secondary">
            <Gauge className="h-3.5 w-3.5" />
            {node
              ? "Waiting for live memory usage from this node."
              : "Select a node to see its available memory."}
          </div>
        )}

        <div className="space-y-1.5">
          <input
            aria-label="Maximum RAM slider"
            aria-valuetext={selectedMb ? formatMemory(selectedMb) : undefined}
            className="memory-range w-full"
            type="range"
            min={MIN_MEMORY_MB}
            max={sliderCeiling ?? MIN_MEMORY_MB}
            step={MEMORY_STEP_MB}
            value={sliderValue}
            style={
              { "--memory-progress": `${sliderPercent}%` } as CSSProperties
            }
            disabled={!sliderEnabled}
            onChange={(event) => setMaximum(Number(event.target.value))}
          />
          <div className="flex items-center justify-between text-[11px] text-text-secondary">
            <span>{formatMemory(MIN_MEMORY_MB)}</span>
            <span>
              {sliderEnabled && sliderCeiling != null
                ? freeMb != null
                  ? `${formatMemory(sliderCeiling)} available`
                  : `${formatMemory(sliderCeiling)} total capacity`
                : "No free range available"}
            </span>
          </div>
        </div>

        {exceedsTotal ? (
          <p className="flex items-start gap-1.5 text-xs text-red-400" role="alert">
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
            This is more than the host&apos;s {formatMemory(totalMb!)} total memory.
          </p>
        ) : exceedsFree ? (
          <p className="flex items-start gap-1.5 text-xs text-amber-400" role="status">
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
            {formatMemory(selectedMb! - freeMb!)} over currently free memory. Lower it or stop other workloads before starting.
          </p>
        ) : remainingMb != null && selectedMb != null ? (
          <p className="text-xs text-text-secondary">
            At the full {formatMemory(selectedMb)} limit, the host keeps {formatMemory(remainingMb)} free.
          </p>
        ) : null}

        {minimumValue !== undefined && onMinimumChange ? (
          <div className="flex flex-col gap-2 border-t border-border/70 pt-3 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <Label htmlFor="minimum-memory">Starting RAM</Label>
              <p className="text-[11px] text-text-secondary">
                Initial Java heap; usually {formatMemory(MIN_MEMORY_MB)}–1 GB is enough.
              </p>
            </div>
            <div className="relative w-full sm:w-28">
              <Input
                id="minimum-memory"
                aria-label="Starting RAM in MB"
                type="number"
                min={MIN_MEMORY_MB}
                max={selectedMb ?? undefined}
                step={MEMORY_STEP_MB}
                value={minimumValue}
                onChange={(event) => onMinimumChange(event.target.value)}
                className="h-8 pr-9 text-right font-mono"
              />
              <span className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 text-[10px] text-text-secondary">
                MB
              </span>
            </div>
          </div>
        ) : null}

        {minimumExceedsMaximum ? (
          <p className="text-xs text-red-400" role="alert">
            Starting RAM cannot be higher than maximum RAM.
          </p>
        ) : null}
      </div>
    </div>
  );
}
