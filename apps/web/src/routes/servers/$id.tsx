import { createRoute, redirect } from "@tanstack/react-router";
import { Route as rootRoute } from "../__root";
import { tabForOldSection } from "@/pages/server/tabs";

// The previous design's server addresses (/servers/<id>/<section>) still
// work: they land on the same place in the new server page.
export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: "/servers/$id/$section",
  beforeLoad: ({ params }) => {
    const { tab, sub } = tabForOldSection(params.section);
    const search = tab === "console" && sub ? { view: sub } : sub ? { sub } : {};
    if (tab === "console") {
      throw redirect({ to: "/s/$id", params: { id: params.id }, search: search as never, replace: true });
    }
    throw redirect({ to: "/s/$id/$tab", params: { id: params.id, tab }, search: search as never, replace: true });
  },
});

export const ServerIndexRedirectRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/servers/$id",
  beforeLoad: ({ params }) => {
    throw redirect({ to: "/s/$id", params: { id: params.id }, replace: true });
  },
});
