import { createRoute, redirect, lazyRouteComponent } from "@tanstack/react-router";
import { Route as rootRoute } from "../__root";

// Component code-split: the server detail page pulls in the terminal (xterm),
// file editor (codemirror), charts, and every server tab — all loaded on
// demand instead of in the initial bundle.
export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/servers/$id/$section",
  component: lazyRouteComponent(() => import("./$id.page"), "ServerDetailPage"),
});

// Bare /servers/:id lands on the dashboard tab, so existing links keep working.
export const ServerIndexRedirectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/servers/$id",
  beforeLoad: ({ params }) => {
    throw redirect({
      to: "/servers/$id/$section",
      params: { id: params.id, section: "dashboard" },
    });
  },
});
