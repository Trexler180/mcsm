import { createRoute, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Server, Activity, ArrowLeftRight, ShieldAlert } from "lucide-react";
import { Route as rootRoute } from "./__root";
import { Header } from "@/components/layout/header";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { AttentionCard } from "@/components/dashboard/attention-card";
import { FleetGrid } from "@/components/dashboard/fleet-grid";
import { NodeHealthCard } from "@/components/dashboard/node-health";
import { ActivityFeed } from "@/components/dashboard/activity-feed";
import { api } from "@/lib/api";

// Placeholder for the region below the stat tiles while the overview loads:
// an attention banner, the fleet grid, and the two bottom panels.
function DashboardSkeleton() {
  return (
    <>
      <Card>
        <CardContent className="py-5">
          <Skeleton className="h-5 w-40" />
          <Skeleton className="mt-3 h-4 w-full max-w-md" />
        </CardContent>
      </Card>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
        {Array.from({ length: 3 }).map((_, i) => (
          <Card key={i}>
            <CardContent className="py-5">
              <div className="flex items-center justify-between gap-3">
                <Skeleton className="h-4 w-28" />
                <Skeleton className="h-5 w-16 rounded-full" />
              </div>
              <Skeleton className="mt-4 h-2 w-full" />
              <Skeleton className="mt-3 h-3 w-24" />
            </CardContent>
          </Card>
        ))}
      </div>

      <div className="grid grid-cols-1 items-start gap-6 lg:grid-cols-2">
        {Array.from({ length: 2 }).map((_, i) => (
          <Card key={i}>
            <CardContent className="space-y-3 py-5">
              <Skeleton className="h-4 w-32" />
              {Array.from({ length: 4 }).map((_, j) => (
                <Skeleton key={j} className="h-3 w-full" />
              ))}
            </CardContent>
          </Card>
        ))}
      </div>
    </>
  );
}

function DashboardPage() {
  const navigate = useNavigate();
  const { data: overview } = useQuery({
    queryKey: ["overview"],
    queryFn: () => api.overview.get(),
    refetchInterval: 10_000,
  });

  // Deep-link straight to a server tab via its own URL.
  const openServer = (id: string, tab?: string) => {
    navigate({
      to: "/servers/$id/$section",
      params: { id, section: tab ?? "dashboard" },
    });
  };

  const counts = overview?.counts;
  const stats = [
    { label: "Servers", value: counts?.servers ?? 0, icon: Server },
    {
      label: "Online",
      value: counts?.online ?? 0,
      icon: Activity,
      accent: true,
    },
    {
      label: "In transition",
      value: counts?.transitioning ?? 0,
      icon: ArrowLeftRight,
    },
    {
      label: "Conflicts",
      value: counts?.conflicts ?? 0,
      icon: ShieldAlert,
      danger: (counts?.conflicts ?? 0) > 0,
    },
  ];

  const nameOf = (serverId: string | null) =>
    (serverId && overview?.servers.find((s) => s.id === serverId)?.name) ||
    "—";

  return (
    <div>
      <Header
        title="Dashboard"
        description="Operations across your Minecraft servers — updates, conflicts, crashes, and backups at a glance"
      />
      <div className="space-y-6 p-4 sm:p-6">
        <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
          {stats.map((s) => (
            <Card
              key={s.label}
              className={
                s.danger
                  ? "border-red-500/30 bg-red-500/[0.03]"
                  : undefined
              }
            >
              {/* Tighter gap on phones so two stat cards fit ~360px without crowding. */}
              <CardContent className="flex items-center gap-3 py-4 sm:gap-4">
                <div
                  className={`flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-lg ${
                    s.danger
                      ? "bg-red-500/15"
                      : s.accent
                        ? "bg-accent/20"
                        : "bg-surface-2"
                  }`}
                >
                  <s.icon
                    className={`h-5 w-5 ${
                      s.danger
                        ? "text-red-400"
                        : s.accent
                          ? "text-accent"
                          : "text-text-secondary"
                    }`}
                  />
                </div>
                <div className="min-w-0">
                  <p
                    className={`text-2xl font-bold tabular-nums ${
                      s.danger ? "text-red-400" : "text-text-primary"
                    }`}
                  >
                    {s.value}
                  </p>
                  <p className="truncate text-sm text-text-secondary">
                    {s.label}
                  </p>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>

        {overview ? (
          <>
            <AttentionCard
              overview={overview}
              onOpenServer={openServer}
              onOpenNodes={() => navigate({ to: "/nodes" })}
            />

            <FleetGrid servers={overview.servers} onOpenServer={openServer} />

            <div className="grid grid-cols-1 items-start gap-6 lg:grid-cols-2">
              <NodeHealthCard nodes={overview.nodes} />
              <ActivityFeed
                activity={overview.activity}
                warnings={overview.warnings}
                nameOf={nameOf}
              />
            </div>
          </>
        ) : (
          <DashboardSkeleton />
        )}
      </div>
    </div>
  );
}

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  component: DashboardPage,
});
