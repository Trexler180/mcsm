import { createRoute, lazyRouteComponent } from "@tanstack/react-router";
import { Route as rootRoute } from "./__root";
import { RequireAdmin } from "@/components/layout/require-admin";

const AuditPage = lazyRouteComponent(
  () => import("./audit.page"),
  "AuditPage",
);

// Component code-split: the page and its heavy imports load on demand,
// keeping them out of the initial bundle.
export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/audit",
  // The API refuses these routes for non-admins; this makes the page say so
  // rather than render a wall of buttons that 403 on click.
  component: () => (
    <RequireAdmin title="Audit Log">
      <AuditPage />
    </RequireAdmin>
  ),
});
