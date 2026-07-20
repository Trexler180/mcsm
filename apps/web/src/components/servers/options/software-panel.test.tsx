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
import type { Server } from "@/lib/types";
import { SoftwareOptionsPanel } from "./software-panel";

vi.mock("@/store/notifications", () => ({
  useNotifications: () => ({ success: vi.fn(), error: vi.fn() }),
}));

const server = {
  id: "server-1",
  node_id: "node-1",
  owner_id: "owner-1",
  name: "Fabric server",
  description: null,
  platform: "fabric",
  mc_version: "26.1.2",
  loader_version: "0.19.3",
  directory_path: "servers/server-1",
  java_binary: "java",
  jvm_args: [],
  port: 25565,
  ram_mb_min: 1024,
  ram_mb_max: 2048,
  status: "offline",
  auto_start: false,
  tags: [],
  settings: {},
  public_status: false,
  public_slug: "",
  created_at: "2026-07-03T00:00:00Z",
  updated_at: "2026-07-03T00:00:00Z",
} satisfies Server;

describe("SoftwareOptionsPanel runtime repair", () => {
  beforeEach(() => {
    vi.spyOn(api.minecraft, "versions").mockResolvedValue([
      { version: "26.1.2", stable: true },
    ]);
    vi.spyOn(api.minecraft, "loaders").mockResolvedValue([
      { version: "0.19.3", stable: true },
      { version: "0.19.2", stable: false },
    ]);
    vi.spyOn(api.servers, "update").mockResolvedValue(server);
    vi.spyOn(api.servers, "reinstall").mockResolvedValue({ status: "reinstalled" });
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  const renderPanel = (srv: Server = server) => {
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={queryClient}>
        <SoftwareOptionsPanel server={srv} />
      </QueryClientProvider>,
    );
  };

  it("applies a changed loader transactionally after confirmation", async () => {
    renderPanel();

    expect(
      screen.getByRole("button", { name: "Repair runtime" }),
    ).toBeInTheDocument();

    await waitFor(() => expect(screen.getAllByRole("combobox")).toHaveLength(2));
    fireEvent.change(screen.getAllByRole("combobox")[1], {
      target: { value: "0.19.2" },
    });

    fireEvent.click(screen.getByRole("button", { name: "Apply & reinstall" }));
    // Nothing is reinstalled until the dialog is confirmed.
    expect(api.servers.reinstall).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Reinstall now" }));
    await waitFor(() =>
      expect(api.servers.reinstall).toHaveBeenCalledWith("server-1", {
        platform: "fabric",
        mc_version: "26.1.2",
        loader_version: "0.19.2",
      }),
    );
  });

  it("repairs the current runtime and can be cancelled", async () => {
    renderPanel();

    fireEvent.click(screen.getByRole("button", { name: "Repair runtime" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(api.servers.reinstall).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: "Repair runtime" }));
    fireEvent.click(screen.getByRole("button", { name: "Reinstall now" }));
    await waitFor(() =>
      expect(api.servers.reinstall).toHaveBeenCalledWith("server-1", {
        platform: "fabric",
        mc_version: "26.1.2",
        loader_version: "0.19.3",
      }),
    );
  });

  it("clears the loader pin when switching to a platform without one", async () => {
    renderPanel();

    await waitFor(() => expect(screen.getAllByRole("combobox")).toHaveLength(2));
    fireEvent.change(screen.getAllByRole("combobox")[0], {
      target: { value: "paper" },
    });
    // The loader field disappears for platforms that resolve their own loader.
    await waitFor(() => expect(screen.getAllByRole("combobox")).toHaveLength(1));

    fireEvent.click(screen.getByRole("button", { name: "Apply & reinstall" }));
    fireEvent.click(screen.getByRole("button", { name: "Reinstall now" }));
    await waitFor(() =>
      expect(api.servers.reinstall).toHaveBeenCalledWith("server-1", {
        platform: "paper",
        mc_version: "26.1.2",
        loader_version: null,
      }),
    );
  });

  it("warns about stopping a running server and import conversion", () => {
    renderPanel({
      ...server,
      status: "online",
      settings: { import: { jar_file: "custom.jar", no_install: true } },
    });

    fireEvent.click(screen.getByRole("button", { name: "Repair runtime" }));
    expect(
      screen.getByText(/stopped first, disconnecting online players/),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/switch to a panel-managed server\.jar/),
    ).toBeInTheDocument();
  });
});
