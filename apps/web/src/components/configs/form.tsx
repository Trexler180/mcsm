import { useEffect, useRef, useState } from "react";
import {
  ChevronRight,
  ChevronDown,
  Plus,
  Trash2,
  Copy,
  Lock,
  ExternalLink,
} from "lucide-react";
import { Input } from "@/components/ui/input";
import {
  type ConfigNode,
  type ConfigFormat,
  type Primitive,
  reindex,
  valueToTree,
  isUrl,
  humanizeFilename,
  pathKey,
} from "@/lib/config";

interface FormContext {
  format: ConfigFormat;
  structural: boolean;
  bump: () => void;
  filter: string;
  /** pathKey of a node to scroll to and flash (from global search). */
  highlight?: string;
  highlightNonce?: number;
}

// Scroll a matched row into view and flash it when it's the search target.
function useHighlight(ctx: FormContext, node: ConfigNode) {
  const ref = useRef<HTMLDivElement>(null);
  const [flash, setFlash] = useState(false);
  const isHi = ctx.highlight !== undefined && ctx.highlight === pathKey(node.path);
  useEffect(() => {
    if (isHi && ref.current) {
      ref.current.scrollIntoView({ block: "center", behavior: "smooth" });
      setFlash(true);
      const t = setTimeout(() => setFlash(false), 1800);
      return () => clearTimeout(t);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ctx.highlight, ctx.highlightNonce]);
  return { ref, flash };
}

function nodeLabel(node: ConfigNode): string {
  if (node.key !== null) return node.key;
  if (node.index !== null) return `#${node.index + 1}`;
  return "";
}

function matchesFilter(node: ConfigNode, q: string): boolean {
  if (!q) return true;
  const label = (node.key ?? "").toString().toLowerCase();
  if (label.includes(q)) return true;
  if (node.doc?.toLowerCase().includes(q)) return true;
  return (node.children ?? []).some((c) => matchesFilter(c, q));
}

// Default value for a new scalar, matching the sibling element types.
function defaultForSiblings(children: ConfigNode[]): Primitive {
  if (children.length > 0 && children.every((c) => c.type === "boolean")) return false;
  if (children.length > 0 && children.every((c) => c.type === "number")) return 0;
  return "";
}

// ── Scalar controls ─────────────────────────────────────────────────────────

function Switch({
  on,
  onToggle,
  title,
}: {
  on: boolean;
  onToggle: () => void;
  title?: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      title={title}
      onClick={onToggle}
      className={`relative inline-flex h-5 w-9 flex-shrink-0 items-center rounded-full transition-colors ${
        on ? "bg-accent" : "bg-surface-2 border border-border"
      }`}
    >
      <span
        className={`inline-block h-4 w-4 transform rounded-full bg-white shadow transition-transform ${
          on ? "translate-x-[18px]" : "translate-x-0.5"
        }`}
      />
    </button>
  );
}

function ScalarControl({
  node,
  ctx,
  disabled,
}: {
  node: ConfigNode;
  ctx: FormContext;
  disabled?: boolean;
}) {
  const [text, setText] = useState(() =>
    node.value === null || node.value === undefined ? "" : String(node.value),
  );

  // Read-only clickable link for documentation URLs.
  if (isUrl(node.value)) {
    const url = String(node.value);
    return (
      <a
        href={url}
        target="_blank"
        rel="noreferrer noopener"
        className="inline-flex max-w-md items-center gap-1.5 truncate text-sm text-accent hover:underline"
      >
        <ExternalLink className="h-3.5 w-3.5 flex-shrink-0" />
        <span className="truncate">{url}</span>
      </a>
    );
  }

  if (!node.editable) {
    return (
      <div className="flex items-center gap-1.5 text-xs text-text-secondary">
        <Lock className="h-3 w-3" />
        <span className="truncate font-mono">{String(node.value)}</span>
      </div>
    );
  }

  if (node.type === "boolean") {
    return (
      <div className={disabled ? "pointer-events-none opacity-40" : ""}>
        <Switch
          on={node.value === true}
          onToggle={() => {
            node.value = !(node.value === true);
            ctx.bump();
          }}
        />
      </div>
    );
  }

  if (node.type === "number") {
    return (
      <Input
        type="number"
        disabled={disabled}
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          const n = Number(e.target.value);
          if (e.target.value.trim() !== "" && !Number.isNaN(n)) {
            node.value = n;
            ctx.bump();
          }
        }}
        className="h-8 w-32 font-mono"
      />
    );
  }

  const multiline = typeof node.value === "string" && node.value.includes("\n");
  if (multiline) {
    return (
      <textarea
        disabled={disabled}
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          node.value = e.target.value;
          node.type = "string";
          ctx.bump();
        }}
        rows={Math.min(8, text.split("\n").length + 1)}
        className="w-full min-w-0 max-w-2xl rounded-md border border-border bg-surface-2 px-3 py-1.5 text-sm font-mono text-text-primary focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-40"
      />
    );
  }

  return (
    <Input
      disabled={disabled}
      value={text}
      placeholder={node.type === "null" ? "null" : ""}
      onChange={(e) => {
        setText(e.target.value);
        if (node.type === "null" && e.target.value === "") {
          node.value = null;
        } else {
          node.value = e.target.value;
          if (node.type === "null") node.type = "string";
        }
        ctx.bump();
      }}
      className="h-8 w-full max-w-md font-mono"
    />
  );
}

// ── Containers ──────────────────────────────────────────────────────────────

function isScalarArray(node: ConfigNode): boolean {
  return (node.children ?? []).every(
    (c) => c.type !== "object" && c.type !== "array",
  );
}

function ArrayView({ node, ctx }: { node: ConfigNode; ctx: FormContext }) {
  const [, setTick] = useState(0);
  const children = node.children ?? [];
  const rerender = () => {
    node.dirty = true;
    reindex(node, node.path);
    ctx.bump();
    setTick((t) => t + 1);
  };

  const removeAt = (i: number) => {
    children.splice(i, 1);
    rerender();
  };
  const addScalar = () => {
    children.push(valueToTree(defaultForSiblings(children), null, children.length, []));
    rerender();
  };
  const duplicate = (i: number) => {
    children.splice(i + 1, 0, structuredClone(children[i]));
    rerender();
  };

  if (children.length === 0) {
    return (
      <div className="flex items-center gap-3 py-1">
        <span className="text-xs italic text-text-secondary">empty list</span>
        {ctx.structural && (
          <button
            onClick={addScalar}
            className="inline-flex items-center gap-1 text-xs text-accent hover:underline"
          >
            <Plus className="h-3 w-3" /> Add
          </button>
        )}
      </div>
    );
  }

  if (isScalarArray(node)) {
    return (
      <div className="space-y-1.5">
        {children.map((child, i) => (
          <div key={i} className="group flex items-center gap-2">
            <span className="w-6 text-right text-xs text-text-secondary">
              {i + 1}
            </span>
            <ScalarControl node={child} ctx={ctx} />
            {ctx.structural && (
              <button
                onClick={() => removeAt(i)}
                className="text-text-secondary opacity-0 transition-opacity hover:text-red-400 group-hover:opacity-100"
                title="Remove"
              >
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
        ))}
        {ctx.structural && (
          <button
            onClick={addScalar}
            className="inline-flex items-center gap-1 text-xs text-accent hover:underline"
          >
            <Plus className="h-3 w-3" /> Add item
          </button>
        )}
      </div>
    );
  }

  return (
    <div className="space-y-2">
      {children.map((child, i) => (
        <div key={i} className="rounded-md border border-border/70 bg-surface-2/30">
          <div className="flex items-center justify-between border-b border-border/50 px-3 py-1.5">
            <span className="text-xs font-medium text-text-secondary">
              Item {i + 1}
            </span>
            {ctx.structural && (
              <div className="flex items-center gap-2">
                <button
                  onClick={() => duplicate(i)}
                  className="text-text-secondary hover:text-text-primary"
                  title="Duplicate"
                >
                  <Copy className="h-3.5 w-3.5" />
                </button>
                <button
                  onClick={() => removeAt(i)}
                  className="text-text-secondary hover:text-red-400"
                  title="Remove"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              </div>
            )}
          </div>
          <div className="p-3">
            <ChildList node={child} ctx={ctx} />
          </div>
        </div>
      ))}
      {ctx.structural && (
        <button
          onClick={() => {
            const template = children[children.length - 1];
            children.push(
              template ? structuredClone(template) : valueToTree({}, null, 0, []),
            );
            rerender();
          }}
          className="inline-flex items-center gap-1 text-xs text-accent hover:underline"
        >
          <Plus className="h-3 w-3" /> Add item
        </button>
      )}
    </div>
  );
}

// A "map" object has dynamic id-like keys (namespace:name) rather than a fixed
// schema — only those get add/remove entry controls. e.g. immersive's
// `dimensions`/`entities` (keys like "minecraft:overworld") qualify; TabTPS's
// `tabDisplayHandler` etc. do not.
function isMapLike(node: ConfigNode): boolean {
  const kids = node.children ?? [];
  return (
    kids.length > 0 &&
    kids.some((c) => typeof c.key === "string" && c.key.includes(":"))
  );
}

function ObjectView({ node, ctx }: { node: ConfigNode; ctx: FormContext }) {
  const [, setTick] = useState(0);
  const children = node.children ?? [];
  const mapLike = ctx.structural && isMapLike(node);
  const rerender = () => {
    node.dirty = true;
    reindex(node, node.path);
    ctx.bump();
    setTick((t) => t + 1);
  };

  const removeKey = (child: ConfigNode) => {
    const i = children.indexOf(child);
    if (i >= 0) children.splice(i, 1);
    rerender();
  };
  const addEntry = () => {
    const key = window.prompt("New entry key:");
    if (!key) return;
    if (children.some((c) => c.key === key)) {
      window.alert(`"${key}" already exists.`);
      return;
    }
    children.push(valueToTree(defaultForSiblings(children), key, null, []));
    rerender();
  };

  return (
    <div className="space-y-1">
      {children
        .filter((c) => matchesFilter(c, ctx.filter))
        .map((child, i) => (
          <NodeRow
            key={`${child.key ?? i}`}
            node={child}
            ctx={ctx}
            onRemove={mapLike ? () => removeKey(child) : undefined}
          />
        ))}
      {mapLike && (
        <button
          onClick={addEntry}
          className="ml-1 mt-1 inline-flex items-center gap-1 text-xs text-text-secondary hover:text-accent"
        >
          <Plus className="h-3 w-3" /> Add entry
        </button>
      )}
    </div>
  );
}

function ChildList({ node, ctx }: { node: ConfigNode; ctx: FormContext }) {
  if (node.type === "array") return <ArrayView node={node} ctx={ctx} />;
  return <ObjectView node={node} ctx={ctx} />;
}

function NodeRow({
  node,
  ctx,
  onRemove,
}: {
  node: ConfigNode;
  ctx: FormContext;
  onRemove?: () => void;
}) {
  const [open, setOpen] = useState(true);
  const [, setTick] = useState(0);
  const { ref, flash } = useHighlight(ctx, node);
  const isContainer = node.type === "object" || node.type === "array";

  if (isContainer) {
    const count = node.children?.length ?? 0;
    return (
      <div className="group rounded-md">
        <div
          ref={ref}
          className={`flex items-center gap-1 rounded-md transition-colors ${
            flash ? "bg-accent/10 ring-1 ring-accent" : ""
          }`}
        >
          <button
            onClick={() => setOpen((o) => !o)}
            className="flex flex-1 items-center gap-1.5 rounded px-1 py-1 text-left hover:bg-surface-2/50"
          >
            {open ? (
              <ChevronDown className="h-3.5 w-3.5 text-text-secondary" />
            ) : (
              <ChevronRight className="h-3.5 w-3.5 text-text-secondary" />
            )}
            <span className="text-sm font-medium text-text-primary">
              {nodeLabel(node)}
            </span>
            <span className="text-xs text-text-secondary">
              {node.type === "array" ? `[${count}]` : `{${count}}`}
            </span>
          </button>
          {onRemove && (
            <button
              onClick={onRemove}
              className="text-text-secondary opacity-0 transition-opacity hover:text-red-400 group-hover:opacity-100"
              title="Remove"
            >
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
        {node.doc && (
          <p className="mb-1 ml-5 whitespace-pre-wrap text-xs text-text-secondary/80">
            {node.doc}
          </p>
        )}
        {open && (
          <div className="ml-4 border-l border-border/50 pl-3">
            <ChildList node={node} ctx={ctx} />
          </div>
        )}
      </div>
    );
  }

  // Scalar row.
  const isDisabled = node.togglable === true && node.disabled === true;
  return (
    <div
      ref={ref}
      className={`group flex flex-col gap-1 rounded-md py-1.5 transition-colors sm:flex-row sm:items-start sm:gap-4 ${
        isDisabled ? "opacity-70" : ""
      } ${flash ? "bg-accent/10 px-2 ring-1 ring-accent" : ""}`}
    >
      <div className="flex items-start gap-2 sm:w-64 sm:flex-shrink-0 sm:pt-1.5">
        {node.togglable && (
          <Switch
            on={!node.disabled}
            title={node.disabled ? "Disabled (commented out)" : "Enabled"}
            onToggle={() => {
              node.disabled = !node.disabled;
              ctx.bump();
              setTick((t) => t + 1);
            }}
          />
        )}
        <div className="min-w-0">
          <label className="block break-words text-sm text-text-primary">
            {nodeLabel(node)}
          </label>
          {node.doc && (
            <p className="mt-0.5 whitespace-pre-wrap text-xs text-text-secondary/80">
              {node.doc}
            </p>
          )}
        </div>
      </div>
      <div className="flex min-w-0 flex-1 items-center gap-2">
        <ScalarControl node={node} ctx={ctx} disabled={isDisabled} />
        {onRemove && (
          <button
            onClick={onRemove}
            className="text-text-secondary opacity-0 transition-opacity hover:text-red-400 group-hover:opacity-100"
            title="Remove"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        )}
      </div>
    </div>
  );
}

export interface ConfigFormProps {
  root: ConfigNode;
  format: ConfigFormat;
  structural: boolean;
  filter: string;
  filename: string;
  onChange: () => void;
  highlight?: string;
  highlightNonce?: number;
}

export function ConfigForm({
  root,
  format,
  structural,
  filter,
  filename,
  onChange,
  highlight,
  highlightNonce,
}: ConfigFormProps) {
  const ctx: FormContext = {
    format,
    structural,
    filter: filter.trim().toLowerCase(),
    bump: onChange,
    highlight,
    highlightNonce,
  };

  // A root that is a bare scalar (e.g. recipe_cooldown.cfg = "50"): use the
  // humanized file name as the field label.
  if (root.type !== "object" && root.type !== "array") {
    return (
      <div className="flex flex-col gap-1 sm:flex-row sm:items-start sm:gap-4">
        <div className="sm:w-64 sm:flex-shrink-0 sm:pt-1.5">
          <label className="block text-sm capitalize text-text-primary">
            {humanizeFilename(filename)}
          </label>
          {root.doc && (
            <p className="mt-0.5 whitespace-pre-wrap text-xs text-text-secondary/80">
              {root.doc}
            </p>
          )}
        </div>
        <ScalarControl node={root} ctx={ctx} />
      </div>
    );
  }

  return <ChildList node={root} ctx={ctx} />;
}

export type { Primitive };
