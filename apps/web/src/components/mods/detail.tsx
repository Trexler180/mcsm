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
} from "lucide-react";
import { Dialog } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
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

// Markdown element overrides so mod bodies match the app theme (no typography
// plugin needed). Links/images open externally and safely.
const mdComponents = {
  h1: (p: any) => <h1 className="text-lg font-semibold text-text-primary mt-4 mb-2" {...p} />,
  h2: (p: any) => <h2 className="text-base font-semibold text-text-primary mt-4 mb-2" {...p} />,
  h3: (p: any) => <h3 className="text-sm font-semibold text-text-primary mt-3 mb-1.5" {...p} />,
  p: (p: any) => <p className="text-sm text-text-secondary leading-relaxed my-2" {...p} />,
  a: (p: any) => (
    <a className="text-accent hover:underline" target="_blank" rel="noreferrer noopener" {...p} />
  ),
  ul: (p: any) => <ul className="list-disc pl-5 my-2 text-sm text-text-secondary space-y-1" {...p} />,
  ol: (p: any) => <ol className="list-decimal pl-5 my-2 text-sm text-text-secondary space-y-1" {...p} />,
  code: (p: any) => (
    <code className="px-1 py-0.5 rounded bg-surface-2 text-xs font-mono text-text-primary" {...p} />
  ),
  pre: (p: any) => (
    <pre className="p-3 rounded bg-surface-2 overflow-x-auto text-xs my-2" {...p} />
  ),
  img: (p: any) => (
    <img className="max-w-full rounded my-2 border border-border" loading="lazy" {...p} />
  ),
  blockquote: (p: any) => (
    <blockquote className="border-l-2 border-border pl-3 italic text-text-secondary my-2" {...p} />
  ),
  table: (p: any) => (
    <div className="overflow-x-auto my-2">
      <table className="text-xs border-collapse" {...p} />
    </div>
  ),
  th: (p: any) => <th className="border border-border px-2 py-1 text-left" {...p} />,
  td: (p: any) => <td className="border border-border px-2 py-1" {...p} />,
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
  const { data: project, isLoading } = useQuery({
    queryKey: ["mod-detail", source, projectId],
    queryFn: () => api.mods.getProject(serverId, projectId, source),
    enabled: open,
    staleTime: 10 * 60_000,
  });

  const featured =
    project?.gallery?.find((g) => g.featured) ?? project?.gallery?.[0];
  const modrinthUrl =
    source === "modrinth"
      ? `https://modrinth.com/${project?.project_type ?? "mod"}/${slug ?? projectId}`
      : `https://www.curseforge.com/minecraft/mc-mods/${slug ?? ""}`;

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={project?.title ?? "Mod details"}
      className="max-w-3xl"
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
        <div className="flex flex-col max-h-[75vh]">
          {/* Header */}
          <div className="flex items-start gap-4 pb-4 border-b border-border">
            {project.icon_url ? (
              <img
                src={project.icon_url}
                alt=""
                className="h-16 w-16 rounded-lg object-cover flex-shrink-0"
              />
            ) : (
              <div className="h-16 w-16 rounded-lg bg-surface-2 flex items-center justify-center flex-shrink-0">
                <Package className="h-7 w-7 text-text-secondary" />
              </div>
            )}
            <div className="min-w-0 flex-1">
              <p className="text-sm text-text-secondary">{project.description}</p>
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
                  <span>
                    Updated {new Date(project.updated).toLocaleDateString()}
                  </span>
                )}
              </div>
              {project.categories?.length > 0 && (
                <div className="flex flex-wrap gap-1 mt-2">
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
            <a href={modrinthUrl} target="_blank" rel="noreferrer noopener">
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

          {/* Body */}
          <div className="flex-1 overflow-y-auto py-3 pr-1">
            {featured && (
              <img
                src={featured.url}
                alt={featured.title}
                className="w-full rounded-lg border border-border mb-4"
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
          </div>

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
