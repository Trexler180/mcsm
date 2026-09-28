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
import type { MCPActionAlert } from "@/lib/mcp-action-alert";
import type { MCPActionRequest } from "@/lib/types";
import { McpApprovalDialog, useMcpApprovalDialog } from "./mcp-approval-dialog";

vi.mock("@/store/notifications", () => ({
  useNotifications: () => ({ success: vi.fn(), error: vi.fn() }),
}));

const alert: MCPActionAlert = {
  requestId: "req-1",
  serverId: "srv-1",
  serverName: "survival",
  action: "restart",
  clientName: "Claude Code",
  reason: "TPS has been under 5 for ten minutes.",
  requiresPassword: true,
  expiresAt: new Date(Date.now() + 600_000).toISOString(),
};

const settled: MCPActionRequest = {
  id: "req-1",
  grant_id: "grant-1",
  client_name: "Claude Code",
  server_id: "srv-1",
  server_name: "survival",
  action: "restart",
  reason: "TPS has been under 5 for ten minutes.",
  untrusted: true,
  status: "executed",
  created_at: new Date().toISOString(),
  expires_at: new Date(Date.now() + 600_000).toISOString(),
  decided_at: new Date().toISOString(),
  executed_at: new Date().toISOString(),
  failure_reason: null,
  requires_password: true,
};

function renderDialog() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <McpApprovalDialog />
    </QueryClientProvider>,
  );
}

describe("McpApprovalDialog", () => {
  beforeEach(() => {
    vi.spyOn(api.auth.mfa, "status").mockResolvedValue({ enabled: false });
    useMcpApprovalDialog.setState({ alert: null });
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders nothing until a request is raised", () => {
    renderDialog();
    expect(screen.queryByRole("button", { name: /approve and run/i })).not.toBeInTheDocument();
  });

  it("collects the step-up and approves the raised request", async () => {
    const approve = vi
      .spyOn(api.mcp.actionRequests, "approve")
      .mockResolvedValue(settled);
    renderDialog();
    useMcpApprovalDialog.getState().open(alert);

    const submit = await screen.findByRole("button", { name: /approve and run/i });
    expect(submit).toBeDisabled();

    fireEvent.change(screen.getByLabelText(/confirm your password/i), {
      target: { value: "hunter2hunter2" },
    });
    fireEvent.click(screen.getByRole("button", { name: /approve and run/i }));

    await waitFor(() =>
      expect(approve).toHaveBeenCalledWith("req-1", {
        password: "hunter2hunter2",
        totp_code: undefined,
      }),
    );
  });

  it("demands a TOTP code too when the account has MFA on", async () => {
    vi.spyOn(api.auth.mfa, "status").mockResolvedValue({ enabled: true });
    renderDialog();
    useMcpApprovalDialog.getState().open(alert);

    fireEvent.change(await screen.findByLabelText(/confirm your password/i), {
      target: { value: "hunter2hunter2" },
    });
    expect(screen.getByRole("button", { name: /approve and run/i })).toBeDisabled();

    fireEvent.change(screen.getByLabelText(/authentication code/i), {
      target: { value: "123456" },
    });
    expect(screen.getByRole("button", { name: /approve and run/i })).toBeEnabled();
  });

  // The agent wrote the reason. It is shown so a human can weigh it, and it has
  // to be labelled as the untrusted thing it is.
  it("shows the agent's reason as untrusted text", async () => {
    renderDialog();
    useMcpApprovalDialog.getState().open(alert);

    expect(
      await screen.findByText(/TPS has been under 5 for ten minutes\./),
    ).toBeInTheDocument();
    expect(screen.getByText(/untrusted text/i)).toBeInTheDocument();
  });

  it("spells out what approving an upgrade sets in motion", async () => {
    renderDialog();
    useMcpApprovalDialog.getState().open({ ...alert, action: "upgrade:26.1.2" });

    expect(await screen.findByText(/Approve: upgrade to 26\.1\.2/)).toBeInTheDocument();
    expect(
      screen.getByText(/creates one full restore-point backup/i),
    ).toBeInTheDocument();
  });

  // A mistyped password should not cost the operator the request; the dialog
  // stays put so they can try again without going to find it.
  it("stays open when approval fails", async () => {
    vi.spyOn(api.mcp.actionRequests, "approve").mockRejectedValue(
      new Error("reauthentication failed"),
    );
    renderDialog();
    useMcpApprovalDialog.getState().open(alert);

    fireEvent.change(await screen.findByLabelText(/confirm your password/i), {
      target: { value: "wrong-password" },
    });
    fireEvent.click(screen.getByRole("button", { name: /approve and run/i }));

    await waitFor(() =>
      expect(screen.getByRole("button", { name: /approve and run/i })).toBeEnabled(),
    );
    expect(screen.getByLabelText(/confirm your password/i)).toBeInTheDocument();
  });
});
