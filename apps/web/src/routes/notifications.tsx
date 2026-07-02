import { createRoute, lazyRouteComponent } from "@tanstack/react-router";
import { Route as rootRoute } from "./__root";

// Component code-split: the page and its heavy imports load on demand,
// keeping them out of the initial bundle.
export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/notifications",
  component: lazyRouteComponent(() => import("./notifications.page"), "NotificationsPage"),
});
