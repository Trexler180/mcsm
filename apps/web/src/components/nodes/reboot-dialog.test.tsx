import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "@/lib/api";
import type { Node } from "@/lib/types";
import { RebootNodeDialog } from "./reboot-dialog";

const notify = { success: vi.fn(), error: vi.fn() };
vi.mock("@/store/notifications", () => ({ useNotifications: () => notify }));

const node = { id: "node-1", name: "host-1" } as Node;

function renderDialog(onClose = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <RebootNodeDialog node={node} serverCount={3} onClose={onClose} />
    </QueryClientProvider>,
  );
  return onClose;
}

describe("RebootNodeDialog", () => {
  beforeEach(() => {
    notify.success.mockReset();
    notify.error.mockReset();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("states the consequences and stays disabled until the password is typed", async () => {
    vi.spyOn(api.auth.mfa, "status").mockResolvedValue({ enabled: false });
    const reboot = vi.spyOn(api.nodes, "reboot");
    renderDialog();

    expect(screen.getByText(/all 3 servers on this node will be stopped/i)).toBeInTheDocument();
    const submit = screen.getByRole("button", { name: /reboot host/i });
    expect(submit).toBeDisabled();
    fireEvent.click(submit);
    expect(reboot).not.toHaveBeenCalled();
  });

  it("sends the step-up and closes once the agent accepts", async () => {
    vi.spyOn(api.auth.mfa, "status").mockResolvedValue({ enabled: true });
    const reboot = vi.spyOn(api.nodes, "reboot").mockResolvedValue({ status: "rebooting" });
    const onClose = renderDialog();

    fireEvent.change(screen.getByLabelText(/confirm your password/i), {
      target: { value: "hunter22hunter" },
    });
    fireEvent.change(await screen.findByLabelText(/authentication code/i), {
      target: { value: " 123456 " },
    });
    fireEvent.click(screen.getByRole("button", { name: /reboot host/i }));

    await waitFor(() =>
      expect(reboot).toHaveBeenCalledWith("node-1", {
        password: "hunter22hunter",
        totp_code: "123456",
      }),
    );
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(notify.success).toHaveBeenCalled();
  });

  it("shows the API's refusal and stays open", async () => {
    vi.spyOn(api.auth.mfa, "status").mockResolvedValue({ enabled: false });
    vi.spyOn(api.nodes, "reboot").mockRejectedValue(
      new Error("agent: host reboot is disabled on this agent (set AGENT_ALLOW_REBOOT=1 to enable it)"),
    );
    const onClose = renderDialog();

    fireEvent.change(screen.getByLabelText(/confirm your password/i), {
      target: { value: "hunter22hunter" },
    });
    fireEvent.click(screen.getByRole("button", { name: /reboot host/i }));

    await waitFor(() =>
      expect(notify.error).toHaveBeenCalledWith(
        "Reboot refused",
        expect.stringContaining("AGENT_ALLOW_REBOOT"),
      ),
    );
    expect(onClose).not.toHaveBeenCalled();
  });
});
