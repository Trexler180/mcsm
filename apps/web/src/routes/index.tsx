import { createRoute, useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Server, Activity, ArrowLeftRight, ShieldAlert } from "lucide-react";
import { Route as rootRoute } from "./__root";
import { Header } from "@/components/layout/header";
import { Card, CardContent } from "@/components/ui/card";
import { AttentionCard } from "@/components/dashboard/attention-card";
import { FleetGrid } from "@/components/dashboard/fleet-grid";
import { NodeHealthCard } from "@/components/dashboard/node-health";
import { ActivityFeed } from "@/components/dashboard/activity-feed";
import { api } from "@/lib/api";

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
            <Card key={s.label}>
              <CardContent className="flex items-center gap-4 py-4">
                <div
                  className={`flex h-10 w-10 items-center justify-center rounded-lg ${
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
                <div>
                  <p className="text-2xl font-bold text-text-primary">
                    {s.value}
                  </p>
                  <p className="text-sm text-text-secondary">{s.label}</p>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>

        {overview && (
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
