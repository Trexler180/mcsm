import { createRoute, lazyRouteComponent } from "@tanstack/react-router";
import { Route as rootRoute } from "./__root";
import { RequireAdmin } from "@/components/layout/require-admin";

const UsersPage = lazyRouteComponent(
  () => import("./users.page"),
  "UsersPage",
);

// Component code-split: the page and its heavy imports load on demand,
// keeping them out of the initial bundle.
export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/users",
  // The API refuses these routes for non-admins; this makes the page say so
  // rather than render a wall of buttons that 403 on click.
  component: () => (
    <RequireAdmin title="Users">
      <UsersPage />
    </RequireAdmin>
  ),
});
