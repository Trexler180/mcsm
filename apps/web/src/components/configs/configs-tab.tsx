import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  FileCog,
  Search,
  RefreshCw,
  Save,
  Code2,
  ListTree,
  AlertTriangle,
  FolderTree,
  Loader2,
  Info,
  CornerDownRight,
  ChevronLeft,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";
import { useNotifications } from "@/store/notifications";
import {
  parseConfig,
  saveConfig,
  detectFormat,
  hasEditableContent,
  pathKey,
  type ConfigNode,
  type ParsedConfig,
  type PathKey,
} from "@/lib/config";
import { ConfigForm } from "./form";
import { RawEditor } from "./raw-editor";

// Extensions we treat as editable config files.
const CONFIG_EXTS = new Set([
  "json",
  "json5",
  "jsonc",
  "toml",
  "yaml",
  "yml",
  "properties",
  "conf",
  "hocon",
  "cfg",
  "ini",
  "txt",
]);

// Directories that typically hold mod configuration.
const CONFIG_ROOTS = ["/config", "/defaultconfigs", "/world/serverconfig"];

interface ConfigFile {
  path: string; // e.g. /config/servercore/config.yml
  name: string; // config.yml
  dir: string; // /config/servercore
  ext: string;
  size: number;
}

const MAX_FILES = 3000;
const MAX_DEPTH = 8;

async function discoverConfigs(serverId: string): Promise<ConfigFile[]> {
  const out: ConfigFile[] = [];
  // One recursive request per root, fired in parallel. The agent walks each
  // tree locally, so we no longer pay a round-trip per directory. Missing
  // roots reject and are skipped.
  const trees = await Promise.all(
    CONFIG_ROOTS.map((root) =>
      api.files.tree(serverId, root, { depth: MAX_DEPTH, max: MAX_FILES }).catch(
        () => null,
      ),
    ),
  );

  for (let i = 0; i < trees.length; i++) {
    const tree = trees[i];
    if (!tree) continue;
    const root = CONFIG_ROOTS[i];
    for (const e of tree.entries) {
      if (e.type !== "file") continue;
      const full = `${root}/${e.path}`;
      const slash = full.lastIndexOf("/");
      addFile(out, full.slice(0, slash), full.slice(slash + 1), e.size);
      if (out.length >= MAX_FILES) break;
    }
  }
  out.sort((a, b) => a.path.localeCompare(b.path));
  return out;
}

function addFile(out: ConfigFile[], dir: string, name: string, size: number) {
  const ext = name.split(".").pop()?.toLowerCase() ?? "";
  if (!CONFIG_EXTS.has(ext)) return;
  out.push({ path: `${dir}/${name}`, name, dir, ext, size });
}

// ── Content search index ──────────────────────────────────────────────────────

interface ContentEntry {
  filePath: string;
  fileName: string;
  nodePath: PathKey[];
  label: string;
  preview: string;
  hay: string; // lowercased haystack: label + value + doc
}

const MAX_INDEX_ENTRIES = 20000;

function collectEntries(node: ConfigNode, file: ConfigFile, out: ContentEntry[]) {
  if (out.length >= MAX_INDEX_ENTRIES) return;
  const isContainer = node.type === "object" || node.type === "array";
  const label =
    node.key !== null
      ? node.key
      : node.index !== null
        ? `#${node.index + 1}`
        : "";
  const preview = isContainer ? "" : String(node.value ?? "");
  if (label && node.path.length > 0) {
    out.push({
      filePath: file.path,
      fileName: file.name,
      nodePath: node.path,
      label,
      preview,
      hay: `${label} ${preview} ${node.doc ?? ""}`.toLowerCase(),
    });
  }
  if (isContainer) for (const c of node.children ?? []) collectEntries(c, file, out);
}

async function buildContentIndex(
  serverId: string,
  files: ConfigFile[],
): Promise<ContentEntry[]> {
  const entries: ContentEntry[] = [];
  let cursor = 0;
  const worker = async () => {
    while (cursor < files.length && entries.length < MAX_INDEX_ENTRIES) {
      const file = files[cursor++];
      try {
        const text = await api.files.readContent(serverId, file.path);
        const parsed = parseConfig(file.name, text);
        if (parsed) collectEntries(parsed.root, file, entries);
      } catch {
        /* skip unreadable files */
      }
    }
  };
  // Bounded concurrency so a big pack doesn't fire hundreds of requests at once.
  await Promise.all(Array.from({ length: Math.min(8, files.length) }, worker));
  return entries;
}

// ── File list + search (left) ─────────────────────────────────────────────────

function FileList({
  files,
  serverId,
  selected,
  onSelect,
}: {
  files: ConfigFile[];
  serverId: string;
  selected: string | null;
  onSelect: (path: string, nodePath?: PathKey[]) => void;
}) {
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  const searching = q.length >= 2;

  const indexQuery = useQuery({
    queryKey: ["config-index", serverId, files.length],
    queryFn: () => buildContentIndex(serverId, files),
    enabled: searching,
    staleTime: 60_000,
  });

  const groups = useMemo(() => {
    const byDir = new Map<string, ConfigFile[]>();
    for (const f of files) {
      const arr = byDir.get(f.dir) ?? [];
      arr.push(f);
      byDir.set(f.dir, arr);
    }
    return [...byDir.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  }, [files]);

  const fileResults = useMemo(
    () => (q ? files.filter((f) => f.path.toLowerCase().includes(q)) : []),
    [files, q],
  );

  const contentResults = useMemo(() => {
    if (!searching) return [];
    const scored: Array<{ e: ContentEntry; rank: number }> = [];
    for (const e of indexQuery.data ?? []) {
      const labelHit = e.label.toLowerCase().includes(q);
      if (!labelHit && !e.hay.includes(q)) continue;
      scored.push({ e, rank: labelHit ? 0 : 1 });
    }
    scored.sort((a, b) => a.rank - b.rank);
    return scored.slice(0, 100).map((s) => s.e);
  }, [indexQuery.data, q, searching]);

  return (
    <div className="flex h-full flex-col">
      <div className="border-b border-border p-3">
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-text-secondary" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search files & options…"
            className="h-8 pl-8 text-xs"
          />
        </div>
        <p className="mt-2 text-xs text-text-secondary">
          {searching
            ? `${fileResults.length} file${fileResults.length !== 1 ? "s" : ""}, ${contentResults.length} option${contentResults.length !== 1 ? "s" : ""}`
            : `${files.length} config file${files.length !== 1 ? "s" : ""}`}
        </p>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto p-2">
        {!searching ? (
          // Directory tree.
          groups.map(([dir, items]) => (
            <div key={dir} className="mb-3">
              <div className="flex items-center gap-1.5 px-2 py-1 text-[11px] uppercase tracking-wide text-text-secondary/70">
                <FolderTree className="h-3 w-3" />
                <span className="truncate">{dir.replace(/^\//, "")}</span>
              </div>
              {items.map((f) => (
                <FileRow
                  key={f.path}
                  file={f}
                  active={selected === f.path}
                  onClick={() => onSelect(f.path)}
                />
              ))}
            </div>
          ))
        ) : (
          <>
            {fileResults.length > 0 && (
              <div className="mb-3">
                <SectionLabel>Files</SectionLabel>
                {fileResults.map((f) => (
                  <FileRow
                    key={f.path}
                    file={f}
                    active={selected === f.path}
                    onClick={() => onSelect(f.path)}
                    showDir
                  />
                ))}
              </div>
            )}

            <div>
              <SectionLabel>
                Inside configs
                {indexQuery.isFetching && (
                  <Loader2 className="ml-1 inline h-3 w-3 animate-spin" />
                )}
              </SectionLabel>
              {contentResults.length === 0 && !indexQuery.isFetching ? (
                <p className="px-2 py-3 text-xs text-text-secondary/70">
                  No matching options.
                </p>
              ) : (
                contentResults.map((e, i) => (
                  <button
                    key={`${e.filePath}|${e.nodePath.join(".")}|${i}`}
                    onClick={() => onSelect(e.filePath, e.nodePath)}
                    className="flex w-full flex-col items-start gap-0.5 rounded px-2 py-1.5 text-left text-text-secondary transition-colors hover:bg-surface-2 hover:text-text-primary"
                  >
                    <span className="flex items-center gap-1.5 text-sm">
                      <CornerDownRight className="h-3 w-3 flex-shrink-0 text-text-secondary/60" />
                      <span className="font-medium text-text-primary">
                        {e.label}
                      </span>
                      {e.preview && (
                        <span className="truncate font-mono text-[11px] text-text-secondary/80">
                          = {e.preview.slice(0, 32)}
                        </span>
                      )}
                    </span>
                    <span className="truncate pl-4 text-[11px] text-text-secondary/60">
                      {e.fileName}
                      {e.nodePath.length > 1 &&
                        ` › ${e.nodePath.slice(0, -1).join(" › ")}`}
                    </span>
                  </button>
                ))
              )}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <div className="px-2 py-1 text-[11px] uppercase tracking-wide text-text-secondary/70">
      {children}
    </div>
  );
}

function FileRow({
  file,
  active,
  onClick,
  showDir,
}: {
  file: ConfigFile;
  active: boolean;
  onClick: () => void;
  showDir?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      className={`flex w-full items-center gap-2 rounded px-2 py-1.5 text-left text-sm transition-colors ${
        active
          ? "bg-accent/15 text-text-primary"
          : "text-text-secondary hover:bg-surface-2 hover:text-text-primary"
      }`}
    >
      <FileCog className="h-3.5 w-3.5 flex-shrink-0" />
      <span className="min-w-0 truncate">
        {file.name}
        {showDir && (
          <span className="ml-1 text-[11px] text-text-secondary/50">
            {file.dir.replace(/^\//, "")}
          </span>
        )}
      </span>
      <span className="ml-auto flex-shrink-0 text-[10px] uppercase text-text-secondary/60">
        {file.ext}
      </span>
    </button>
  );
}

// ── Per-file editor (right) ──────────────────────────────────────────────────

function ConfigEditor({
  serverId,
  path,
  target,
  targetNonce,
}: {
  serverId: string;
  path: string;
  target?: PathKey[];
  targetNonce: number;
}) {
  const { success, error } = useNotifications();
  const name = path.split("/").pop() ?? path;
  const format = detectFormat(name);

  const contentQuery = useQuery({
    queryKey: ["file-content", serverId, path],
    queryFn: () => api.files.readContent(serverId, path),
    retry: false,
    // Keep recently-opened files cached so flipping between them is instant;
    // an explicit reload still refetches via refetch().
    staleTime: 30_000,
    gcTime: 10 * 60_000,
  });

  const [baseText, setBaseText] = useState("");
  const [parsed, setParsed] = useState<ParsedConfig | null>(null);
  const [mode, setMode] = useState<"structured" | "raw">("structured");
  const [rawText, setRawText] = useState("");
  const [dirty, setDirty] = useState(false);
  const [, setVersion] = useState(0);
  const [resetKey, setResetKey] = useState(0);
  const [filter, setFilter] = useState("");
  const [saving, setSaving] = useState(false);

  // (Re)initialize whenever fresh content arrives.
  useEffect(() => {
    if (contentQuery.data === undefined) return;
    const text = contentQuery.data;
    setBaseText(text);
    setRawText(text);
    const p = parseConfig(name, text);
    setParsed(p);
    setMode(p && hasEditableContent(p.root) ? "structured" : "raw");
    setDirty(false);
    setFilter("");
    setResetKey((k) => k + 1);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [contentQuery.data, path]);

  // When a search target arrives, make sure the field is visible.
  useEffect(() => {
    if (!target) return;
    setFilter("");
    if (parsed && hasEditableContent(parsed.root)) setMode("structured");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [targetNonce]);

  const bump = () => {
    setVersion((v) => v + 1);
    setDirty(true);
  };

  const toRaw = () => {
    const text =
      mode === "structured" && parsed ? saveConfig(parsed, baseText) : rawText;
    setRawText(text);
    setMode("raw");
    setResetKey((k) => k + 1);
  };

  const toStructured = () => {
    const text = rawText;
    const p = parseConfig(name, text);
    if (!p) {
      error("Can't parse", `Fix the ${format} syntax, or keep editing raw.`);
      return;
    }
    setBaseText(text);
    setParsed(p);
    setMode("structured");
    setResetKey((k) => k + 1);
  };

  const save = async () => {
    const newText =
      mode === "structured" && parsed ? saveConfig(parsed, baseText) : rawText;
    setSaving(true);
    try {
      await api.files.writeContent(serverId, path, newText);
      setBaseText(newText);
      setRawText(newText);
      const p = parseConfig(name, newText);
      if (p) setParsed(p);
      setDirty(false);
      setResetKey((k) => k + 1);
      success("Saved", path);
    } catch (e) {
      error("Save failed", e instanceof Error ? e.message : "Unknown error");
    } finally {
      setSaving(false);
    }
  };

  const structuredAvailable = parsed !== null && hasEditableContent(parsed.root);
  const highlight = target ? pathKey(target) : undefined;

  return (
    <div className="flex h-full flex-col">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-2 border-b border-border bg-surface px-4 py-2">
        {/* Cap the path so it truncates instead of widening the wrap row on
            narrow screens; mono paths have no natural break points. */}
        <span className="max-w-full truncate font-mono text-sm text-text-secondary sm:max-w-xs">
          {path}
        </span>
        <Badge variant="muted" className="uppercase">
          {format === "unknown" || format === "scalar"
            ? name.split(".").pop()
            : format}
        </Badge>

        <div className="ml-auto flex items-center gap-2">
          {mode === "structured" && structuredAvailable && (
            <div className="relative hidden xl:block">
              <Search className="absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-text-secondary" />
              <Input
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                placeholder="Filter fields…"
                className="h-8 w-44 pl-8 text-xs"
              />
            </div>
          )}

          <div className="flex overflow-hidden rounded-md border border-border">
            <button
              onClick={() => mode === "raw" && toStructured()}
              disabled={!structuredAvailable}
              className={`inline-flex items-center gap-1.5 px-3 py-1.5 text-xs transition-colors ${
                mode === "structured"
                  ? "bg-accent/15 text-text-primary"
                  : "text-text-secondary hover:bg-surface-2 disabled:opacity-40"
              }`}
              title={
                structuredAvailable ? "Form editor" : "No structured view available"
              }
            >
              <ListTree className="h-3.5 w-3.5" /> Form
            </button>
            <button
              onClick={() => mode === "structured" && toRaw()}
              className={`inline-flex items-center gap-1.5 px-3 py-1.5 text-xs transition-colors ${
                mode === "raw"
                  ? "bg-accent/15 text-text-primary"
                  : "text-text-secondary hover:bg-surface-2"
              }`}
              title="Raw text"
            >
              <Code2 className="h-3.5 w-3.5" /> Raw
            </button>
          </div>

          <Button
            size="sm"
            variant="ghost"
            onClick={() => contentQuery.refetch()}
            title="Reload from disk"
          >
            <RefreshCw className="h-3.5 w-3.5" />
          </Button>
          <Button size="sm" onClick={save} loading={saving} disabled={!dirty}>
            <Save className="h-3.5 w-3.5" /> {dirty ? "Save" : "Saved"}
          </Button>
        </div>
      </div>

      {/* Hints */}
      {mode === "structured" && parsed && !parsed.structural && (
        <div className="flex items-center gap-2 border-b border-border bg-surface/50 px-4 py-1.5 text-xs text-text-secondary">
          <Info className="h-3.5 w-3.5 flex-shrink-0" />
          Editing values in place — comments and formatting are preserved.
        </div>
      )}
      {!structuredAvailable && parsed === null && contentQuery.data !== undefined && (
        <div className="flex items-center gap-2 border-b border-border bg-yellow-500/10 px-4 py-1.5 text-xs text-yellow-300/90">
          <AlertTriangle className="h-3.5 w-3.5 flex-shrink-0" />
          Couldn't parse as {format}. Showing raw text.
        </div>
      )}

      {/* Body */}
      <div className="min-h-0 flex-1 overflow-hidden">
        {contentQuery.isLoading ? (
          <div className="flex h-full items-center justify-center">
            <Loader2 className="h-5 w-5 animate-spin text-accent" />
          </div>
        ) : contentQuery.isError ? (
          <div className="flex h-full items-center justify-center text-sm text-text-secondary">
            Failed to read file.
          </div>
        ) : mode === "structured" && parsed && structuredAvailable ? (
          <div className="h-full overflow-y-auto px-5 py-4">
            <ConfigForm
              key={`form-${resetKey}`}
              root={parsed.root}
              format={parsed.format}
              structural={parsed.structural}
              filter={filter}
              filename={name}
              onChange={bump}
              highlight={highlight}
              highlightNonce={targetNonce}
            />
          </div>
        ) : (
          <RawEditor
            filename={name}
            value={rawText}
            onChange={(v) => {
              setRawText(v);
              setDirty(true);
            }}
            resetKey={`${path}:${resetKey}`}
          />
        )}
      </div>
    </div>
  );
}

// ── Tab shell ─────────────────────────────────────────────────────────────────

export function ConfigsTab({ serverId }: { serverId: string }) {
  const [selected, setSelected] = useState<string | null>(null);
  const [target, setTarget] = useState<{ path: string; node: PathKey[] } | null>(
    null,
  );
  const [targetNonce, setTargetNonce] = useState(0);

  const select = (path: string, nodePath?: PathKey[]) => {
    setSelected(path);
    setTarget(nodePath ? { path, node: nodePath } : null);
    setTargetNonce((n) => n + 1);
  };

  const {
    data: files = [],
    isLoading,
    isError,
    refetch,
    isFetching,
  } = useQuery({
    queryKey: ["config-files", serverId],
    queryFn: () => discoverConfigs(serverId),
    // The scan is cheap to keep around; serve it instantly when revisiting the
    // tab and only refetch on an explicit rescan.
    staleTime: 5 * 60_000,
    gcTime: 30 * 60_000,
  });

  return (
    <div className="flex h-full min-w-0">
      {/* Master/detail like the Files tab: show the file list OR the editor (a
          288px sidebar beside a sliver of editor is unusable); only show both
          side by side once there's room. The server view's app + section
          sidebars already eat ~448px, so a 72-list + editor split only fits from
          xl — below that, one pane at a time. */}
      <aside
        className={`${selected ? "hidden xl:flex" : "flex"} w-full flex-shrink-0 flex-col border-border bg-surface/40 xl:w-72 xl:border-r`}
      >
        <div className="flex items-center justify-between border-b border-border px-3 py-2">
          <h2 className="text-sm font-semibold text-text-primary">Configs</h2>
          <Button size="sm" variant="ghost" onClick={() => refetch()} title="Rescan">
            <RefreshCw
              className={`h-3.5 w-3.5 ${isFetching ? "animate-spin" : ""}`}
            />
          </Button>
        </div>
        {isLoading ? (
          <div className="flex flex-1 items-center justify-center">
            <Loader2 className="h-5 w-5 animate-spin text-accent" />
          </div>
        ) : isError ? (
          <p className="p-4 text-xs text-text-secondary">Failed to scan configs.</p>
        ) : files.length === 0 ? (
          <div className="flex flex-1 flex-col items-center justify-center gap-2 p-6 text-center">
            <FileCog className="h-8 w-8 text-text-secondary/30" />
            <p className="text-sm text-text-secondary">No config files found</p>
            <p className="text-xs text-text-secondary/70">
              Install some mods and start the server once to generate configs.
            </p>
          </div>
        ) : (
          <FileList
            files={files}
            serverId={serverId}
            selected={selected}
            onSelect={select}
          />
        )}
      </aside>

      {/* Editor pane: full-width once a file is picked, hidden until then so the
          list owns the screen (until the split engages at xl). */}
      <main className={`${selected ? "flex" : "hidden xl:flex"} min-w-0 flex-1 flex-col`}>
        {selected ? (
          <>
            {/* Back to the file list — only while single-pane (list is hidden). */}
            <button
              onClick={() => setSelected(null)}
              className="flex flex-shrink-0 items-center gap-1.5 border-b border-border bg-surface px-4 py-2 text-sm text-text-secondary hover:text-text-primary xl:hidden"
            >
              <ChevronLeft className="h-4 w-4" />
              Configs
            </button>
            <div className="min-h-0 flex-1">
              <ConfigEditor
                key={selected}
                serverId={serverId}
                path={selected}
                target={target?.path === selected ? target.node : undefined}
                targetNonce={targetNonce}
              />
            </div>
          </>
        ) : (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-text-secondary">
            <FileCog className="h-10 w-10 opacity-30" />
            <p className="text-sm">Select a config file to edit</p>
            <p className="max-w-sm text-center text-xs text-text-secondary/70">
              Mod configs are parsed into an editable form automatically. Toggle to
              Raw any time for full text control.
            </p>
          </div>
        )}
      </main>
    </div>
  );
}
