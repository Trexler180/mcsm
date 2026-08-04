import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "@/lib/api";
import type { InstalledMod, ModImpact } from "@/lib/types";
import { ModRemovalDialog } from "./removal-dialog";

vi.mock("@/store/notifications", () => ({
  useNotifications: () => ({ success: vi.fn(), error: vi.fn() }),
}));

const fabricAPI: InstalledMod = {
  id: "mod-lib",
  server_id: "server-1",
  source: "modrinth",
  source_id: "P7dR8mSH",
  version_id: "ver-1",
  name: "Fabric API",
  version: "0.150.0",
  file_name: "fabric-api-0.150.0.jar",
  sha256: null,
  pinned: false,
  enabled: true,
  install_path: "/mods",
  installed_as_dep: false,
  installed_at: "2026-01-01T00:00:00Z",
  required_by: ["Clumps"],
  orphaned: false,
};

const clear: ModImpact = {
  mod_id: "mod-lib",
  name: "Fabric API",
  enabled: true,
  required: [],
  optional: [],
  orphaning: [],
  unchecked: 0,
  checked: true,
};

const breaking: ModImpact = {
  ...clear,
  required: [
    {
      mod_id: "mod-a",
      name: "Clumps",
      version: "26.1.2.1",
      source: "modrinth",
      source_id: "pid-a",
      enabled: true,
      dependency_type: "required",
    },
  ],
  optional: [
    {
      mod_id: "mod-c",
      name: "Nice To Have",
      version: "1.0",
      source: "modrinth",
      source_id: "pid-c",
      enabled: true,
      dependency_type: "optional",
    },
  ],
  orphaning: ["Tiny Helper"],
  unchecked: 2,
};

function renderDialog(
  impact: ModImpact,
  mode: "uninstall" | "disable" = "uninstall",
) {
  vi.spyOn(api.mods, "dependents").mockResolvedValue(impact);
  vi.spyOn(api.mods, "getProject").mockResolvedValue({} as never);
  const onConfirm = vi.fn();
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <ModRemovalDialog
        open
        serverId="server-1"
        mod={fabricAPI}
        mode={mode}
        pending={false}
        onClose={vi.fn()}
        onConfirm={onConfirm}
      />
    </QueryClientProvider>,
  );
  return onConfirm;
}

describe("ModRemovalDialog", () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("warns with the affected content when something requires the mod", async () => {
    renderDialog(breaking);

    expect(await screen.findByText("Dependency warning")).toBeInTheDocument();
    expect(
      screen.getByText(/required by 1 other item/i),
    ).toBeInTheDocument();
    expect(screen.getByText("Clumps")).toBeInTheDocument();
    // Optional users are called out separately — they don't break.
    expect(screen.getByText(/Nice To Have/)).toBeInTheDocument();
    expect(screen.getByText(/uses it optionally/)).toBeInTheDocument();
    // And the panel admits what it couldn't check.
    expect(
      screen.getByText(/2 other installed items couldn't be checked/),
    ).toBeInTheDocument();
  });

  it("defaults to taking the dependents offline with it", async () => {
    const onConfirm = renderDialog(breaking);

    fireEvent.click(await screen.findByText("Uninstall anyway"));
    expect(onConfirm).toHaveBeenCalledWith({ disableDependents: true });
  });

  it("respects unticking the disable-dependents option", async () => {
    const onConfirm = renderDialog(breaking);

    fireEvent.click(await screen.findByRole("checkbox"));
    fireEvent.click(screen.getByText("Uninstall anyway"));
    expect(onConfirm).toHaveBeenCalledWith({ disableDependents: false });
  });

  it("stays an ordinary confirmation when nothing depends on the mod", async () => {
    const onConfirm = renderDialog(clear);

    // The title reads the same while the check is still running, so wait for
    // the verdict itself before asserting the dialog stayed plain.
    expect(
      await screen.findByText(/Nothing else installed requires it/),
    ).toBeInTheDocument();
    expect(screen.getByText("Uninstall content")).toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();

    fireEvent.click(screen.getByText("Uninstall"));
    expect(onConfirm).toHaveBeenCalledWith({ disableDependents: false });
  });

  it("survives a payload whose lists came back null", async () => {
    renderDialog({
      ...clear,
      required: null,
      optional: null,
      orphaning: null,
    } as unknown as ModImpact);

    expect(
      await screen.findByText(/Nothing else installed requires it/),
    ).toBeInTheDocument();
  });

  it("frames the same warning as a disable when that's the action", async () => {
    renderDialog(breaking, "disable");

    expect(await screen.findByText("Dependency warning")).toBeInTheDocument();
    expect(screen.getByText(/Disabling it may stop them loading/)).toBeInTheDocument();
    expect(screen.getByText("Disable anyway")).toBeInTheDocument();
  });

  it("won't let a removal be confirmed before the check answers", async () => {
    vi.spyOn(api.mods, "dependents").mockReturnValue(new Promise(() => {}));
    vi.spyOn(api.mods, "getProject").mockResolvedValue({} as never);
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={qc}>
        <ModRemovalDialog
          open
          serverId="server-1"
          mod={fabricAPI}
          mode="uninstall"
          pending={false}
          onClose={vi.fn()}
          onConfirm={vi.fn()}
        />
      </QueryClientProvider>,
    );

    await waitFor(() =>
      expect(screen.getByText(/Checking what depends on/)).toBeInTheDocument(),
    );
    expect(screen.getByText("Uninstall").closest("button")).toBeDisabled();
  });
});
