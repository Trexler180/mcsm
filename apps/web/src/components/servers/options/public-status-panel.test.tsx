import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Server } from "@/lib/types";
import { PublicStatusPanel } from "./public-status-panel";

vi.mock("@/store/notifications", () => ({
  useNotifications: () => ({ success: vi.fn(), error: vi.fn() }),
}));

afterEach(cleanup);

describe("PublicStatusPanel", () => {
  it("renders safely when an older API omits public status fields", () => {
    const legacyServer = {
      id: "server-1",
      name: "Legacy server",
      public_status: undefined,
      public_slug: undefined,
    } as unknown as Server;
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });

    render(
      <QueryClientProvider client={queryClient}>
        <PublicStatusPanel server={legacyServer} />
      </QueryClientProvider>,
    );

    expect(screen.getByText("Public Status Page")).toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: /Enable the public status page/ }),
    ).not.toBeChecked();
    expect(screen.getByRole("button", { name: "Saved" })).toBeDisabled();
  });
});
