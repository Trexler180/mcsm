import { useEffect, useReducer, useRef, useState } from "react";
import {
  Save,
  Loader2,
  ChevronRight,
  ChevronDown,
  Braces,
  List,
  Hash,
  Type,
  Brackets,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { useNotifications } from "@/store/notifications";
import { api } from "@/lib/api";
import {
  parseNbt,
  serializeNbt,
  TAG_NAMES,
  TAG_BYTE,
  TAG_SHORT,
  TAG_INT,
  TAG_LONG,
  TAG_FLOAT,
  TAG_DOUBLE,
  TAG_BYTE_ARRAY,
  TAG_STRING,
  TAG_LIST,
  TAG_COMPOUND,
  TAG_INT_ARRAY,
  TAG_LONG_ARRAY,
  type NbtTag,
  type NbtEntry,
  type NbtRoot,
} from "@/lib/nbt";

interface DatViewerProps {
  serverId: string;
  path: string;
}

const INT_TYPES = new Set([TAG_BYTE, TAG_SHORT, TAG_INT]);
const FLOAT_TYPES = new Set([TAG_FLOAT, TAG_DOUBLE]);
// Inclusive signed ranges per integer tag, used to clamp edits.
const INT_RANGE: Record<number, [number, number]> = {
  [TAG_BYTE]: [-128, 127],
  [TAG_SHORT]: [-32768, 32767],
  [TAG_INT]: [-2147483648, 2147483647],
};

function typeColor(type: number): string {
  if (type === TAG_STRING) return "text-green-400";
  if (type === TAG_COMPOUND) return "text-yellow-400";
  if (type === TAG_LIST) return "text-purple-400";
  if (INT_TYPES.has(type) || type === TAG_LONG) return "text-sky-400";
  if (FLOAT_TYPES.has(type)) return "text-orange-400";
  return "text-text-secondary";
}

function TypeIcon({ type }: { type: number }) {
  const cls = `h-3.5 w-3.5 flex-shrink-0 ${typeColor(type)}`;
  if (type === TAG_COMPOUND) return <Braces className={cls} />;
  if (type === TAG_LIST) return <List className={cls} />;
  if (
    type === TAG_BYTE_ARRAY ||
    type === TAG_INT_ARRAY ||
    type === TAG_LONG_ARRAY
  )
    return <Brackets className={cls} />;
  if (type === TAG_STRING) return <Type className={cls} />;
  return <Hash className={cls} />;
}

function isContainer(type: number): boolean {
  return type === TAG_COMPOUND || type === TAG_LIST;
}

// Render the editable scalar control for a leaf tag. Edits mutate the tag in
// place and call onChange() to flag the document dirty + re-render.
function ScalarEditor({ tag, onChange }: { tag: NbtTag; onChange: () => void }) {
  const base =
    "bg-surface-2 border border-border rounded px-2 py-0.5 text-xs font-mono text-text-primary focus:outline-none focus:border-accent";

  if (tag.type === TAG_STRING) {
    return (
      <input
        className={`${base} w-64`}
        value={tag.value as string}
        onChange={(e) => {
          tag.value = e.target.value;
          onChange();
        }}
      />
    );
  }

  if (INT_TYPES.has(tag.type)) {
    const [min, max] = INT_RANGE[tag.type];
    return (
      <input
        type="number"
        className={`${base} w-32`}
        value={tag.value as number}
        onChange={(e) => {
          let v = parseInt(e.target.value, 10);
          if (Number.isNaN(v)) v = 0;
          tag.value = Math.max(min, Math.min(max, v));
          onChange();
        }}
      />
    );
  }

  if (FLOAT_TYPES.has(tag.type)) {
    return (
      <input
        type="number"
        step="any"
        className={`${base} w-40`}
        value={tag.value as number}
        onChange={(e) => {
          const v = parseFloat(e.target.value);
          tag.value = Number.isNaN(v) ? 0 : v;
          onChange();
        }}
      />
    );
  }

  if (tag.type === TAG_LONG) {
    return (
      <input
        className={`${base} w-48`}
        value={(tag.value as bigint).toString()}
        onChange={(e) => {
          try {
            tag.value = BigInt(e.target.value.trim() || "0");
            onChange();
          } catch {
            /* keep last valid value on bad input */
          }
        }}
      />
    );
  }

  // Numeric arrays: comma/space separated. byte/int -> number, long -> bigint.
  const isLongArr = tag.type === TAG_LONG_ARRAY;
  const arr = tag.value as Array<number | bigint>;
  return (
    <textarea
      rows={Math.min(6, Math.max(1, Math.ceil(arr.length / 12)))}
      className={`${base} w-96 resize-y`}
      defaultValue={arr.join(", ")}
      onBlur={(e) => {
        const parts = e.target.value
          .split(/[\s,]+/)
          .map((s) => s.trim())
          .filter(Boolean);
        try {
          tag.value = parts.map((p) => (isLongArr ? BigInt(p) : Number(p)));
          onChange();
        } catch {
          /* ignore parse errors */
        }
      }}
    />
  );
}

function childEntries(tag: NbtTag): NbtEntry[] {
  if (tag.type === TAG_COMPOUND) return tag.value as NbtEntry[];
  if (tag.type === TAG_LIST)
    return (tag.value as NbtTag[]).map((t, i) => ({ name: `[${i}]`, tag: t }));
  return [];
}

function TreeNode({
  name,
  tag,
  depth,
  onChange,
}: {
  name: string;
  tag: NbtTag;
  depth: number;
  onChange: () => void;
}) {
  const [open, setOpen] = useState(depth < 1);
  const pad = { paddingLeft: `${depth * 14 + 4}px` };
  const container = isContainer(tag.type);
  const count = container ? childEntries(tag).length : 0;

  return (
    <div>
      <div
        className="flex items-center gap-2 py-1 hover:bg-surface-2/40 rounded"
        style={pad}
      >
        {container ? (
          <button
            onClick={() => setOpen((o) => !o)}
            className="text-text-secondary hover:text-text-primary"
          >
            {open ? (
              <ChevronDown className="h-3.5 w-3.5" />
            ) : (
              <ChevronRight className="h-3.5 w-3.5" />
            )}
          </button>
        ) : (
          <span className="w-3.5" />
        )}
        <TypeIcon type={tag.type} />
        <span className="text-xs font-mono text-text-primary">{name}</span>
        <span className="text-[10px] uppercase tracking-wide text-text-secondary/60">
          {TAG_NAMES[tag.type]}
        </span>
        {container ? (
          <span className="text-[11px] text-text-secondary">
            {count} {count === 1 ? "entry" : "entries"}
          </span>
        ) : (
          <ScalarEditor tag={tag} onChange={onChange} />
        )}
      </div>
      {container && open && (
        <div>
          {childEntries(tag).map((e, i) => (
            <TreeNode
              key={`${e.name}-${i}`}
              name={e.name}
              tag={e.tag}
              depth={depth + 1}
              onChange={onChange}
            />
          ))}
        </div>
      )}
    </div>
  );
}

export function DatViewer({ serverId, path }: DatViewerProps) {
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);
  const rootRef = useRef<NbtRoot | null>(null);
  const gzippedRef = useRef(false);
  const [, force] = useReducer((n: number) => n + 1, 0);
  const { success, error } = useNotifications();

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setLoadError(null);
    setDirty(false);
    rootRef.current = null;
    api.files
      .readBytes(serverId, path)
      .then((bytes) => parseNbt(bytes))
      .then(({ root, gzipped }) => {
        if (cancelled) return;
        rootRef.current = root;
        gzippedRef.current = gzipped;
        force();
      })
      .catch((e: Error) => {
        if (!cancelled) setLoadError(e.message);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [serverId, path]);

  const onChange = () => {
    setDirty(true);
    force();
  };

  const save = async () => {
    if (!rootRef.current) return;
    setSaving(true);
    try {
      const bytes = await serializeNbt(rootRef.current, gzippedRef.current);
      await api.files.writeBytes(serverId, path, bytes);
      setDirty(false);
      success("Saved");
    } catch (e) {
      error("Save failed", e instanceof Error ? e.message : "Unknown error");
    } finally {
      setSaving(false);
    }
  };

  const root = rootRef.current;

  return (
    <div className="flex flex-col h-full">
      <div className="flex-shrink-0 flex items-center justify-between px-4 py-2 border-b border-border bg-surface">
        <div className="flex min-w-0 items-center gap-2">
          <span className="text-sm text-text-secondary font-mono truncate">
            {path}
          </span>
          {gzippedRef.current && (
            <span className="text-[10px] uppercase tracking-wide text-text-secondary/60 border border-border rounded px-1.5 py-0.5">
              gzip
            </span>
          )}
          <span className="text-[10px] uppercase tracking-wide text-text-secondary/60 border border-border rounded px-1.5 py-0.5">
            NBT
          </span>
        </div>
        <Button size="sm" onClick={save} loading={saving} disabled={!root || !dirty}>
          <Save className="h-3.5 w-3.5" />
          Save
        </Button>
      </div>

      <div className="flex-1 min-h-0 overflow-auto bg-[#0f0f0f] px-3 py-2">
        {loading ? (
          <div className="flex items-center justify-center h-full">
            <Loader2 className="h-5 w-5 text-accent animate-spin" />
          </div>
        ) : loadError ? (
          <div className="flex flex-col items-center justify-center h-full gap-2 text-center px-6">
            <p className="text-sm text-red-400">Not a valid NBT file</p>
            <p className="text-xs text-text-secondary font-mono">{loadError}</p>
          </div>
        ) : root ? (
          <TreeNode
            name={root.name || "(root)"}
            tag={root.tag}
            depth={0}
            onChange={onChange}
          />
        ) : null}
      </div>
    </div>
  );
}
