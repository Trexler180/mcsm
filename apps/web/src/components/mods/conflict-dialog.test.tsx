import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "@/lib/api";
import type { ModConflict } from "@/lib/types";
import { ModConflictDialog } from "./conflict-dialog";
import { ServerPermissionsProvider } from "@/lib/server-permissions";

vi.mock("@/store/notifications", () => ({
  useNotifications: () => ({ success: vi.fn(), error: vi.fn() }),
}));

// What the agent produces for a Fabric server whose mods need Fabric API but
// where Fabric API was never installed — the loader names it by bare mod id.
const missingFabricAPI: ModConflict = {
  detected: true,
  kind: "missing_dependency",
  summary: "Fabric API is required but not installed",
  suggestions: [
    {
      action: "install",
      mod_id: "fabric",
      mod_name: "Fabric API",
      requirements: ["any version"],
      required_by: ["AppleSkin", "Sodium"],
    },
  ],
  raw: ["\t - Install fabric, any version."],
  detected_at: 1234,
};

function renderDialog(conflict: ModConflict, onClose = vi.fn()) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <QueryClientProvider client={qc}>
      {/* The dialog's actions are permission-gated, so it only works inside a
          permissions provider — as an owner, who holds everything. */}
      <ServerPermissionsProvider
        permissions={{ owner: true, global_admin: false, permissions: [] }}
      >
        <ModConflictDialog
          serverId="server-1"
          conflict={conflict}
          onClose={onClose}
        />
      </ServerPermissionsProvider>
    </QueryClientProvider>,
  );
  return onClose;
}

describe("ModConflictDialog missing dependency", () => {
  beforeEach(() => {
    vi.spyOn(api.mods, "resolveMissing").mockResolvedValue([
      {
        mod_id: "fabric",
        found: true,
        project_id: "P7dR8mSH",
        slug: "fabric-api",
        title: "Fabric API",
        version_id: "ver-1",
        version_number: "0.100.0+1.21.1",
      },
    ]);
    vi.spyOn(api.mods, "install").mockResolvedValue(undefined);
    vi.spyOn(api.servers, "start").mockResolvedValue(undefined as never);
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("resolves the bare loader mod id to a named project and says what needs it", async () => {
    renderDialog(missingFabricAPI);

    await waitFor(() =>
      expect(api.mods.resolveMissing).toHaveBeenCalledWith("server-1", [
        "fabric",
      ]),
    );
    expect(await screen.findByText("0.100.0+1.21.1")).toBeInTheDocument();
    expect(
      screen.getByText(/Required by: AppleSkin, Sodium/),
    ).toBeInTheDocument();
    // A missing dependency is fixed by installing, never by disabling.
    expect(screen.queryByText(/Disable selected/)).not.toBeInTheDocument();
  });

  it("installs the pinned version with its dependencies, then restarts", async () => {
    const onClose = renderDialog(missingFabricAPI);

    fireEvent.click(await screen.findByText(/Install & restart/));

    await waitFor(() =>
      expect(api.mods.install).toHaveBeenCalledWith(
        "server-1",
        "modrinth",
        "P7dR8mSH",
        "ver-1",
        true,
      ),
    );
    await waitFor(() =>
      expect(api.servers.start).toHaveBeenCalledWith("server-1"),
    );
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("does not restart when only installing", async () => {
    renderDialog(missingFabricAPI);

    fireEvent.click(await screen.findByText(/Install only/));

    await waitFor(() => expect(api.mods.install).toHaveBeenCalled());
    expect(api.servers.start).not.toHaveBeenCalled();
  });

  it("explains an unusable dependency instead of offering to install it", async () => {
    vi.spyOn(api.mods, "resolveMissing").mockResolvedValue([
      {
        mod_id: "fabric",
        found: true,
        project_id: "P7dR8mSH",
        slug: "fabric-api",
        title: "Fabric API",
        error: "Fabric API has no build for fabric 1.99",
      },
    ]);

    renderDialog(missingFabricAPI);

    expect(
      await screen.findByText(/no build for fabric 1\.99/),
    ).toBeInTheDocument();
    expect(screen.queryByText(/Install & restart/)).not.toBeInTheDocument();
  });
});
