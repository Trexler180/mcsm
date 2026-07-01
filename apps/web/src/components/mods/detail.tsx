import { useState, type ComponentProps } from "react";
import { useQuery } from "@tanstack/react-query";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import {
  Loader2,
  Package,
  ExternalLink,
  Download,
  Heart,
  Bug,
  Code2,
  BookOpen,
  Monitor,
  Server as ServerIcon,
  Calendar,
  Tag,
} from "lucide-react";
import { Dialog } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import type { ModSource } from "@/lib/types";

interface ModDetailDialogProps {
  open: boolean;
  onClose: () => void;
  serverId: string;
  source: ModSource;
  projectId: string;
  slug?: string;
  /** Optional install button rendered in the footer. */
  onInstall?: () => void;
  installing?: boolean;
  installed?: boolean;
}

function formatDownloads(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(0)}K`;
  return String(n);
}

// envLabel turns a Modrinth side value ("required"/"optional"/"unsupported"/
// "unknown") into a short human label, or null when it shouldn't be shown.
function envLabel(side?: string): string | null {
  switch (side) {
    case "required":
      return "Required";
    case "optional":
      return "Optional";
    case "unsupported":
      return "Unsupported";
    default:
      return null;
  }
}

// Markdown element overrides so mod bodies match the app theme (no typography
// plugin needed). Links/images open externally and safely.
const mdComponents = {
  h1: (p: ComponentProps<"h1">) => <h1 className="text-lg font-semibold text-text-primary mt-4 mb-2" {...p} />,
  h2: (p: ComponentProps<"h2">) => <h2 className="text-base font-semibold text-text-primary mt-4 mb-2" {...p} />,
  h3: (p: ComponentProps<"h3">) => <h3 className="text-sm font-semibold text-text-primary mt-3 mb-1.5" {...p} />,
  p: (p: ComponentProps<"p">) => <p className="text-sm text-text-secondary leading-relaxed my-2" {...p} />,
  a: (p: ComponentProps<"a">) => (
    <a className="text-accent hover:underline" target="_blank" rel="noreferrer noopener" {...p} />
  ),
  ul: (p: ComponentProps<"ul">) => <ul className="list-disc pl-5 my-2 text-sm text-text-secondary space-y-1" {...p} />,
  ol: (p: ComponentProps<"ol">) => <ol className="list-decimal pl-5 my-2 text-sm text-text-secondary space-y-1" {...p} />,
  code: (p: ComponentProps<"code">) => (
    <code className="px-1 py-0.5 rounded bg-surface-2 text-xs font-mono text-text-primary" {...p} />
  ),
  pre: (p: ComponentProps<"pre">) => (
    <pre className="p-3 rounded bg-surface-2 overflow-x-auto text-xs my-2" {...p} />
  ),
  img: (p: ComponentProps<"img">) => (
    <img className="max-w-full rounded my-2 border border-border" loading="lazy" {...p} />
  ),
  blockquote: (p: ComponentProps<"blockquote">) => (
    <blockquote className="border-l-2 border-border pl-3 italic text-text-secondary my-2" {...p} />
  ),
  table: (p: ComponentProps<"table">) => (
    <div className="overflow-x-auto my-2">
      <table className="min-w-full text-xs border-collapse" {...p} />
    </div>
  ),
  th: (p: ComponentProps<"th">) => <th className="border border-border px-2 py-1 text-left" {...p} />,
  td: (p: ComponentProps<"td">) => <td className="border border-border px-2 py-1" {...p} />,
};

export function ModDetailDialog({
  open,
  onClose,
  serverId,
  source,
  projectId,
  slug,
  onInstall,
  installing,
  installed,
}: ModDetailDialogProps) {
  const [tab, setTab] = useState("description");

  const { data: project, isLoading } = useQuery({
    queryKey: ["mod-detail", source, projectId],
    queryFn: () => api.mods.getProject(serverId, projectId, source),
    enabled: open,
    staleTime: 10 * 60_000,
  });

  // Versions load lazily — only once the user opens the Versions tab.
  const { data: versions = [], isFetching: loadingVersions } = useQuery({
    queryKey: ["mod-detail-versions", source, projectId],
    queryFn: () => api.mods.getVersions(serverId, projectId, "", "", source),
    enabled: open && tab === "versions",
    staleTime: 10 * 60_000,
  });

  const gallery = project?.gallery ?? [];
  const featured = gallery.find((g) => g.featured) ?? gallery[0];

  // External project page per source. Hangar slugs are "owner/slug"; SpigotMC
  // resource pages resolve by numeric id alone.
  const projectUrl =
    source === "modrinth"
      ? `https://modrinth.com/${project?.project_type ?? "mod"}/${slug ?? projectId}`
      : source === "hangar"
        ? `https://hangar.papermc.io/${project?.slug ?? slug ?? projectId}`
        : source === "spigotmc"
          ? `https://www.spigotmc.org/resources/${projectId}`
          : `https://www.curseforge.com/minecraft/mc-mods/${slug ?? ""}`;

  const clientEnv = envLabel(project?.client_side);
  const serverEnv = envLabel(project?.server_side);

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={project?.title ?? "Mod details"}
      className="!max-w-7xl"
    >
      {isLoading ? (
        <div className="flex justify-center py-16">
          <Loader2 className="h-6 w-6 animate-spin text-accent" />
        </div>
      ) : !project ? (
        <p className="text-sm text-text-secondary py-8 text-center">
          Could not load mod details.
        </p>
      ) : (
        <div className="flex h-[82vh] max-h-[calc(100vh-8rem)] flex-col">
          {/* Header */}
          <div className="flex flex-col gap-4 pb-4 border-b border-border sm:flex-row sm:items-start">
            {project.icon_url ? (
              <img
                src={project.icon_url}
                alt=""
                className="h-20 w-20 rounded-lg object-cover flex-shrink-0"
              />
            ) : (
              <div className="h-20 w-20 rounded-lg bg-surface-2 flex items-center justify-center flex-shrink-0">
                <Package className="h-8 w-8 text-text-secondary" />
              </div>
            )}
            <div className="min-w-0 flex-1 space-y-2">
              <p className="text-sm text-text-secondary leading-relaxed">{project.description}</p>
              <div className="flex flex-wrap items-center gap-3 mt-2 text-xs text-text-secondary">
                <span className="flex items-center gap-1">
                  <Download className="h-3.5 w-3.5" />
                  {formatDownloads(project.downloads)}
                </span>
                {typeof project.followers === "number" && (
                  <span className="flex items-center gap-1">
                    <Heart className="h-3.5 w-3.5" />
                    {formatDownloads(project.followers)}
                  </span>
                )}
                {project.updated && (
                  <span className="flex items-center gap-1">
                    <Calendar className="h-3.5 w-3.5" />
                    Updated {new Date(project.updated).toLocaleDateString()}
                  </span>
                )}
                {project.project_type && (
                  <span className="flex items-center gap-1">
                    <Tag className="h-3.5 w-3.5" />
                    {project.project_type}
                  </span>
                )}
                {clientEnv && (
                  <span className="flex items-center gap-1" title="Client side">
                    <Monitor className="h-3.5 w-3.5" />
                    Client: {clientEnv}
                  </span>
                )}
                {serverEnv && (
                  <span className="flex items-center gap-1" title="Server side">
                    <ServerIcon className="h-3.5 w-3.5" />
                    Server: {serverEnv}
                  </span>
                )}
              </div>
              {project.categories?.length > 0 && (
                <div className="flex max-h-20 flex-wrap gap-1 overflow-y-auto pr-1">
                  {project.categories.map((c) => (
                    <span
                      key={c}
                      className="text-xs px-1.5 py-0.5 rounded bg-surface-2 text-text-secondary border border-border/50"
                    >
                      {c}
                    </span>
                  ))}
                </div>
              )}
            </div>
          </div>

          {/* Links */}
          <div className="flex flex-wrap gap-2 py-3 border-b border-border">
            <a href={projectUrl} target="_blank" rel="noreferrer noopener">
              <Button size="sm" variant="outline">
                <ExternalLink className="h-3.5 w-3.5" /> Project page
              </Button>
            </a>
            {project.source_url && (
              <a href={project.source_url} target="_blank" rel="noreferrer noopener">
                <Button size="sm" variant="ghost">
                  <Code2 className="h-3.5 w-3.5" /> Source
                </Button>
              </a>
            )}
            {project.issues_url && (
              <a href={project.issues_url} target="_blank" rel="noreferrer noopener">
                <Button size="sm" variant="ghost">
                  <Bug className="h-3.5 w-3.5" /> Issues
                </Button>
              </a>
            )}
            {project.wiki_url && (
              <a href={project.wiki_url} target="_blank" rel="noreferrer noopener">
                <Button size="sm" variant="ghost">
                  <BookOpen className="h-3.5 w-3.5" /> Wiki
                </Button>
              </a>
            )}
          </div>

          {/* Tabs */}
          <Tabs value={tab} onValueChange={setTab} className="flex flex-col flex-1 min-h-0">
            <TabsList>
              <TabsTrigger value="description">Description</TabsTrigger>
              <TabsTrigger value="gallery">
                Gallery{gallery.length > 0 ? ` (${gallery.length})` : ""}
              </TabsTrigger>
              <TabsTrigger value="versions">Versions</TabsTrigger>
            </TabsList>

            {/* Description */}
            <TabsContent value="description" className="flex-1 overflow-y-auto py-3 pr-2">
              {featured && (
                <img
                  src={featured.url}
                  alt={featured.title}
                  className="w-full max-h-[46vh] rounded-lg border border-border bg-surface-2 object-contain mb-4"
                  loading="lazy"
                />
              )}
              {project.body ? (
                <ReactMarkdown remarkPlugins={[remarkGfm]} components={mdComponents}>
                  {project.body}
                </ReactMarkdown>
              ) : (
                <p className="text-sm text-text-secondary">
                  No detailed description provided.
                </p>
              )}
            </TabsContent>

            {/* Gallery */}
            <TabsContent value="gallery" className="flex-1 overflow-y-auto py-3 pr-2">
              {gallery.length === 0 ? (
                <p className="text-sm text-text-secondary py-8 text-center">
                  No gallery images for this project.
                </p>
              ) : (
                <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
                  {gallery.map((img, i) => (
                    <figure key={`${img.url}-${i}`} className="space-y-1">
                      <a href={img.url} target="_blank" rel="noreferrer noopener">
                        <img
                          src={img.url}
                          alt={img.title}
                          className="w-full max-h-80 rounded-lg border border-border bg-surface-2 object-contain"
                          loading="lazy"
                        />
                      </a>
                      {(img.title || img.description) && (
                        <figcaption className="text-xs text-text-secondary">
                          {img.title && (
                            <span className="text-text-primary font-medium">
                              {img.title}
                            </span>
                          )}
                          {img.title && img.description ? " — " : ""}
                          {img.description}
                        </figcaption>
                      )}
                    </figure>
                  ))}
                </div>
              )}
            </TabsContent>

            {/* Versions */}
            <TabsContent value="versions" className="flex-1 overflow-y-auto py-3 pr-2">
              {loadingVersions ? (
                <div className="flex justify-center py-12">
                  <Loader2 className="h-5 w-5 animate-spin text-accent" />
                </div>
              ) : versions.length === 0 ? (
                <p className="text-sm text-text-secondary py-8 text-center">
                  No versions found.
                </p>
              ) : (
                <div className="divide-y divide-border/50">
                  {versions.map((v) => (
                    <div
                      key={v.id}
                      className="flex items-start justify-between gap-3 py-2.5"
                    >
                      <div className="min-w-0">
                        <p className="text-sm font-medium text-text-primary truncate">
                          {v.name || v.version_number}
                        </p>
                        <p className="text-xs text-text-secondary mt-0.5">
                          {v.version_number}
                          {v.loaders.length > 0 && ` · ${v.loaders.join(", ")}`}
                        </p>
                        <div className="flex flex-wrap gap-1 mt-1">
                          {v.game_versions.map((g) => (
                            <span
                              key={g}
                              className="text-[10px] px-1.5 py-0.5 rounded bg-surface-2 text-text-secondary border border-border/50"
                            >
                              {g}
                            </span>
                          ))}
                        </div>
                      </div>
                      {v.date_published && (
                        <span className="text-xs text-text-secondary flex-shrink-0 whitespace-nowrap">
                          {new Date(v.date_published).toLocaleDateString()}
                        </span>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </TabsContent>
          </Tabs>

          {/* Footer install action */}
          {onInstall && (
            <div className="flex justify-end pt-3 border-t border-border">
              {installed ? (
                <span className="text-sm text-green-400 px-2 py-1">
                  Installed
                </span>
              ) : (
                <Button onClick={onInstall} loading={installing}>
                  <Download className="h-3.5 w-3.5" /> Install
                </Button>
              )}
            </div>
          )}
        </div>
      )}
    </Dialog>
  );
}
