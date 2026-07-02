import { useEffect, useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  KeyRound,
  Loader2,
  Search,
  SlidersHorizontal,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { api } from "@/lib/api";
import { sanitizeSvg } from "@/lib/sanitize";
import type {
  InstalledMod,
  ModProjectType,
  ModSortIndex,
  ModSource,
} from "@/lib/types";
import {
  LOADERS,
  PAGE_SIZE,
  PROJECT_TYPES,
  SORTS,
  type DetailTarget,
} from "./shared";
import { SearchHitCard } from "./search-hit-card";
import { FilterSelect } from "./filter-select";

interface BrowseTabProps {
  serverId: string;
  /** Whether this tab is the visible one. Content stays mounted either way so
   *  filters, results, and pagination survive tab switches. */
  active: boolean;
  /** Server platform, doubles as the Modrinth loader facet for mods. */
  loader: string;
  mcVersion: string;
  /** Server platform, used to fetch the Minecraft version list. */
  platform: string;
  curseforgeEnabled: boolean;
  installed: InstalledMod[];
  onShowDetails: (target: DetailTarget) => void;
  /** Reports the current result total so the container's tab bar can show it
   *  (null until the first search resolves). */
  onTotalHits: (n: number | null) => void;
}

export function BrowseTab({
  serverId,
  active,
  loader,
  mcVersion,
  platform,
  curseforgeEnabled,
  installed,
  onShowDetails,
  onTotalHits,
}: BrowseTabProps) {
  const [query, setQuery] = useState("");
  const [searchInput, setSearchInput] = useState("");
  const [source, setSource] = useState<ModSource>("modrinth");
  const [projectType, setProjectType] = useState<ModProjectType>("mod");
  const [sortIndex, setSortIndex] = useState<ModSortIndex>("relevance");
  const [selectedCats, setSelectedCats] = useState<string[]>([]);
  const [mcFilter, setMcFilter] = useState<string>(mcVersion);
  const [loaderFilter, setLoaderFilter] = useState<string>(loader);
  // Default to anything that runs on the server (client-optional included).
  const [environment, setEnvironment] = useState<string>("");
  const [offset, setOffset] = useState(0);
  const [hideInstalled, setHideInstalled] = useState(false);
  // Browse filters live in a slide-in drawer on phones; static sidebar on ≥md.
  const [filtersOpen, setFiltersOpen] = useState(false);

  // Mods filter by modloader; plugins/datapacks/etc. don't.
  const browseLoader = projectType === "mod" ? loaderFilter : "";

  // Sort vocabularies differ per source: CurseForge has no relevance/follows
  // ranking (both map to popularity server-side), Hangar's "follows" is stars,
  // Spiget's is likes (and its relevance is just download popularity).
  const sortOptions: { value: ModSortIndex; label: string }[] =
    source === "curseforge"
      ? [
          { value: "relevance", label: "Popularity" },
          { value: "downloads", label: "Downloads" },
          { value: "newest", label: "Newest" },
          { value: "updated", label: "Updated" },
        ]
      : source === "hangar"
        ? [
            { value: "relevance", label: "Relevance" },
            { value: "downloads", label: "Downloads" },
            { value: "follows", label: "Stars" },
            { value: "newest", label: "Newest" },
            { value: "updated", label: "Updated" },
          ]
        : source === "spigotmc"
          ? [
              { value: "relevance", label: "Popularity" },
              { value: "downloads", label: "Downloads" },
              { value: "follows", label: "Likes" },
              { value: "newest", label: "Newest" },
              { value: "updated", label: "Updated" },
            ]
          : SORTS;

  // Reset paging whenever any browse dimension changes.
  useEffect(() => {
    setOffset(0);
  }, [
    query,
    selectedCats,
    sortIndex,
    projectType,
    mcFilter,
    loaderFilter,
    environment,
    source,
  ]);

  const { data: searchResult, isFetching: searching } = useQuery({
    queryKey: [
      "mod-search",
      serverId,
      source,
      query,
      browseLoader,
      mcFilter,
      projectType,
      sortIndex,
      selectedCats.join(","),
      environment,
      offset,
    ],
    queryFn: () =>
      api.mods.search(serverId, {
        query,
        source,
        loader: browseLoader,
        mcVersion: mcFilter,
        projectType,
        categories: selectedCats,
        index: sortIndex,
        environment,
        limit: PAGE_SIZE,
        offset,
      }),
    // Skip CurseForge requests until a key is configured — the UI shows an
    // add-a-key prompt instead, so the call would just 502.
    enabled: source !== "curseforge" || curseforgeEnabled,
  });

  // Surface the result total to the container's tab bar.
  const reportedTotal = searchResult ? searchResult.total_hits : null;
  useEffect(() => {
    onTotalHits(reportedTotal);
  }, [reportedTotal, onTotalHits]);

  const { data: categories = [] } = useQuery({
    queryKey: ["mod-categories", source, projectType],
    queryFn: () => api.mods.categories(serverId, projectType, source),
    staleTime: 60 * 60_000,
    enabled: source !== "curseforge" || curseforgeEnabled,
  });

  const groupedCats = useMemo(() => {
    const m = new Map<string, typeof categories>();
    for (const c of categories) {
      if (!m.has(c.header)) m.set(c.header, []);
      m.get(c.header)!.push(c);
    }
    return [...m.entries()];
  }, [categories]);

  const { data: mcVersions = [] } = useQuery({
    queryKey: ["mc-versions-mods", platform],
    queryFn: () => api.minecraft.versions(platform),
    staleTime: 60 * 60_000,
  });

  const installedProjectIds = new Set(installed.map((m) => m.source_id ?? ""));

  const handleSearch = (e: React.FormEvent) => {
    e.preventDefault();
    setQuery(searchInput.trim());
  };

  const toggleCat = (name: string) => {
    setSelectedCats((prev) =>
      prev.includes(name) ? prev.filter((c) => c !== name) : [...prev, name],
    );
  };

  const totalHits = searchResult?.total_hits ?? 0;
  const totalPages = Math.max(1, Math.ceil(totalHits / PAGE_SIZE));
  const page = Math.floor(offset / PAGE_SIZE) + 1;

  // Optionally drop already-installed projects from the browse list. Filtering is
  // client-side (per page), so the page/total counts still reflect the full set.
  const visibleHits = (searchResult?.hits ?? []).filter(
    (h) => !hideInstalled || !installedProjectIds.has(h.project_id),
  );

  // Build the numbered pagination window (max 7 entries, current centred).
  const pageNumbers = useMemo(() => {
    const span = 5;
    let start = Math.max(1, page - Math.floor(span / 2));
    const end = Math.min(totalPages, start + span - 1);
    start = Math.max(1, end - span + 1);
    const nums: number[] = [];
    for (let i = start; i <= end; i++) nums.push(i);
    return nums;
  }, [page, totalPages]);

  return (
    /* ── Browse: search bar on top, results + right filter sidebar ─── */
    <div
      className={`${active ? "flex" : "hidden"} flex-col xl:flex-row flex-1 min-h-0`}
    >
      {/* Main column */}
      <div className="flex-1 min-w-0 min-h-0 flex flex-col">
        <div className="flex-shrink-0 px-4 py-3 border-b border-border bg-surface space-y-2">
          <form onSubmit={handleSearch} className="flex gap-2">
            <div className="relative flex-1">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-text-secondary pointer-events-none" />
              <Input
                className="pl-9"
                placeholder={
                  source === "curseforge"
                    ? "Search CurseForge…"
                    : source === "hangar"
                      ? "Search Hangar…"
                      : source === "spigotmc"
                        ? "Search SpigotMC…"
                        : "Search content…"
                }
                value={searchInput}
                onChange={(e) => setSearchInput(e.target.value)}
              />
            </div>
            <Button type="submit" size="md" loading={searching}>
              Search
            </Button>
          </form>
          <div className="flex items-center justify-between gap-2">
            <label className="flex items-center gap-2 text-xs text-text-secondary cursor-pointer select-none">
              <input
                type="checkbox"
                className="accent-accent"
                checked={hideInstalled}
                onChange={(e) => setHideInstalled(e.target.checked)}
              />
              Hide already installed content
            </label>
            <Button
              type="button"
              size="sm"
              variant="outline"
              className="xl:hidden flex-shrink-0"
              onClick={() => setFiltersOpen(true)}
            >
              <SlidersHorizontal className="h-3.5 w-3.5" />
              Filters
            </Button>
          </div>
        </div>

        <div className="flex-1 min-h-0 overflow-y-auto p-4">
          {source === "curseforge" && !curseforgeEnabled ? (
            <div className="max-w-md mx-auto text-center py-12 space-y-3">
              <div className="w-11 h-11 rounded-lg bg-accent/10 flex items-center justify-center mx-auto">
                <KeyRound className="h-5 w-5 text-accent" />
              </div>
              <p className="text-sm text-text-primary font-medium">
                CurseForge search needs an API key
              </p>
              <p className="text-sm text-text-secondary">
                Add a CurseForge API key in Settings to search and browse
                CurseForge. Modrinth works without one, and installed
                CurseForge mods still update normally.
              </p>
              <Link
                to="/settings"
                className="inline-flex items-center gap-1.5 text-sm text-accent hover:underline"
              >
                <KeyRound className="h-3.5 w-3.5" />
                Open Settings → Integrations
              </Link>
            </div>
          ) : searching ? (
            <div className="flex justify-center py-8">
              <Loader2 className="h-5 w-5 animate-spin text-accent" />
            </div>
          ) : !searchResult || visibleHits.length === 0 ? (
            <div className="text-center py-12 text-text-secondary">
              <p className="text-sm">
                {hideInstalled && (searchResult?.hits.length ?? 0) > 0
                  ? "All results on this page are already installed"
                  : query
                    ? `No results for "${query}"`
                    : "No results"}
              </p>
            </div>
          ) : (
            <>
              <div className="space-y-2">
                {visibleHits.map((hit) => (
                  <SearchHitCard
                    key={hit.project_id}
                    hit={hit}
                    serverId={serverId}
                    source={source}
                    isModpack={
                      projectType === "modpack" && source === "modrinth"
                    }
                    projectType={projectType}
                    loader={browseLoader}
                    mcVersion={mcFilter}
                    serverLoader={loader}
                    serverMc={mcVersion}
                    installedIds={installedProjectIds}
                    onShowDetails={() =>
                      onShowDetails({
                        source,
                        projectId: hit.project_id,
                        slug: hit.slug,
                        isModpack:
                          projectType === "modpack" && source === "modrinth",
                        installed: installedProjectIds.has(hit.project_id),
                        hit,
                        projectType,
                      })
                    }
                  />
                ))}
              </div>

              {/* Numbered pagination */}
              {totalPages > 1 && (
                <div className="flex items-center justify-center gap-1 pt-4">
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={offset === 0}
                    onClick={() =>
                      setOffset((o) => Math.max(0, o - PAGE_SIZE))
                    }
                  >
                    Prev
                  </Button>
                  {pageNumbers[0] > 1 && (
                    <span className="px-1 text-xs text-text-secondary">…</span>
                  )}
                  {pageNumbers.map((n) => (
                    <button
                      key={n}
                      onClick={() => setOffset((n - 1) * PAGE_SIZE)}
                      className={`h-7 min-w-7 px-2 rounded text-xs transition-colors ${
                        n === page
                          ? "bg-accent text-black font-medium"
                          : "text-text-secondary hover:bg-surface-2"
                      }`}
                    >
                      {n}
                    </button>
                  ))}
                  {pageNumbers[pageNumbers.length - 1] < totalPages && (
                    <span className="px-1 text-xs text-text-secondary">…</span>
                  )}
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={page >= totalPages}
                    onClick={() => setOffset((o) => o + PAGE_SIZE)}
                  >
                    Next
                  </Button>
                </div>
              )}
            </>
          )}
        </div>
      </div>

      {/* Dim the results behind the filter drawer while it overlays. */}
      {filtersOpen && (
        <div
          className="fixed inset-0 z-30 bg-black/50 xl:hidden"
          onClick={() => setFiltersOpen(false)}
        />
      )}

      {/* Right filter sidebar — slide-in drawer until the pane is wide
          enough for a static column. The server view's two sidebars keep the
          content pane narrow until the viewport hits xl, so the static
          sidebar only earns its place there; below that it's the drawer. */}
      <aside
        className={`${
          filtersOpen
            ? "fixed inset-y-0 right-0 z-40 w-80 max-w-[85vw] shadow-2xl"
            : "hidden"
        } xl:static xl:z-auto xl:block xl:w-60 xl:max-w-none xl:shadow-none xl:flex-shrink-0 border-l border-border bg-surface overflow-y-auto p-4 space-y-4`}
      >
        <div className="flex items-center justify-between xl:hidden">
          <span className="text-sm font-medium text-text-primary">
            Filters
          </span>
          <button
            onClick={() => setFiltersOpen(false)}
            className="text-text-secondary hover:text-text-primary"
            title="Close filters"
          >
            <X className="h-5 w-5" />
          </button>
        </div>
        <FilterSelect
          label="Source"
          value={source}
          onChange={(v) => {
            setSource(v as ModSource);
            // Category filter values and available sorts differ per source.
            setSelectedCats([]);
            if (v === "curseforge" && sortIndex === "follows") {
              setSortIndex("relevance");
            }
            // Hangar and SpigotMC host plugins exclusively.
            if (v === "hangar" || v === "spigotmc") {
              setProjectType("plugin");
            }
          }}
          title="Mod source"
        >
          <option value="modrinth">Modrinth</option>
          <option value="curseforge">
            CurseForge{curseforgeEnabled ? "" : " (needs API key)"}
          </option>
          <option value="hangar">Hangar (PaperMC)</option>
          <option value="spigotmc">SpigotMC</option>
        </FilterSelect>

        <FilterSelect
          label="Content type"
          value={projectType}
          onChange={(v) => {
            setProjectType(v as ModProjectType);
            setSelectedCats([]);
          }}
        >
          {(source === "hangar" || source === "spigotmc"
            ? PROJECT_TYPES.filter((t) => t.value === "plugin")
            : PROJECT_TYPES
          ).map((t) => (
            <option key={t.value} value={t.value}>
              {t.label}
            </option>
          ))}
        </FilterSelect>

        <FilterSelect
          label="Sort by"
          value={sortIndex}
          onChange={(v) => setSortIndex(v as ModSortIndex)}
        >
          {sortOptions.map((s) => (
            <option key={s.value} value={s.value}>
              {s.label}
            </option>
          ))}
        </FilterSelect>

        {/* Spiget has no version facet, so the filter would silently do
            nothing on SpigotMC. */}
        {source !== "spigotmc" && (
          <FilterSelect
            label="Game version"
            value={mcFilter}
            onChange={setMcFilter}
            title="Minecraft version"
          >
            <option value="">Any version</option>
            {mcVersions.map((v) => (
              <option key={v.version} value={v.version}>
                {v.version}
              </option>
            ))}
          </FilterSelect>
        )}

        {projectType === "mod" && (
          <FilterSelect
            label="Loader"
            value={loaderFilter}
            onChange={setLoaderFilter}
            title="Loader"
          >
            <option value="">Any loader</option>
            {LOADERS.map((l) => (
              <option key={l} value={l}>
                {l}
              </option>
            ))}
          </FilterSelect>
        )}

        {/* CurseForge has no side metadata, so the environment facet only
            exists for Modrinth. */}
        {source === "modrinth" && (
          <FilterSelect
            label="Environment"
            value={environment}
            onChange={setEnvironment}
            title="Environment"
          >
            <option value="">Server (any)</option>
            <option value="server_only">Server only</option>
            <option value="client_server">Client + Server</option>
            <option value="client">Client</option>
            <option value="any">Any</option>
          </FilterSelect>
        )}

        {/* Category tags */}
        {groupedCats.length > 0 && (
          <div className="space-y-3 pt-1">
            <div className="flex items-center justify-between">
              <span className="text-[10px] uppercase tracking-wide text-text-secondary">
                Categories
              </span>
              {selectedCats.length > 0 && (
                <button
                  onClick={() => setSelectedCats([])}
                  className="text-[10px] text-accent hover:underline"
                >
                  Clear ({selectedCats.length})
                </button>
              )}
            </div>
            {groupedCats.map(([header, cats]) => (
              <div key={header} className="space-y-1.5">
                <p className="text-[10px] uppercase tracking-wide text-text-secondary/70">
                  {header}
                </p>
                <div className="flex flex-wrap gap-1">
                  {cats.map((c) => {
                    // CF filters by numeric id, Modrinth by tag name.
                    const value = c.id ?? c.name;
                    const active = selectedCats.includes(value);
                    return (
                      <button
                        key={value}
                        onClick={() => toggleCat(value)}
                        className={`inline-flex items-center gap-1 text-xs px-2 py-1 rounded border transition-colors ${
                          active
                            ? "bg-accent/15 text-accent border-accent/40"
                            : "bg-surface-2 text-text-secondary border-border hover:text-text-primary"
                        }`}
                      >
                        {c.icon.startsWith("http") ? (
                          <img
                            src={c.icon}
                            alt=""
                            className="h-3.5 w-3.5 rounded-sm object-cover"
                          />
                        ) : c.icon && sanitizeSvg(c.icon) ? (
                          <span
                            className="h-3.5 w-3.5 inline-flex items-center justify-center [&_svg]:h-full [&_svg]:w-full"
                            dangerouslySetInnerHTML={{ __html: sanitizeSvg(c.icon) }}
                          />
                        ) : null}
                        {c.name}
                      </button>
                    );
                  })}
                </div>
              </div>
            ))}
          </div>
        )}
      </aside>
    </div>
  );
}
