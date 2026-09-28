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
import type {
  MCPActionRequest,
  MCPConnectionInfo,
  MCPGrant,
} from "@/lib/types";
import { RemoteAgentsCard } from "./remote-agents";

vi.mock("@/store/notifications", () => ({
  useNotifications: () => ({ success: vi.fn(), error: vi.fn() }),
}));

const connection: MCPConnectionInfo = {
  configured: true,
  url: "https://panel.test/api/v1/mcp",
  claude_code:
    "claude mcp add --transport http servermanager https://panel.test/api/v1/mcp",
  codex_config:
    '[mcp_servers.servermanager]\nurl = "https://panel.test/api/v1/mcp"',
  hermes_config:
    'mcp_servers:\n  servermanager:\n    url: "https://panel.test/api/v1/mcp"\n    auth: oauth',
  scopes: ["mcp:servers.read", "mcp:actions.request"],
  max_days: 90,
  default_days: 30,
};

const grant: MCPGrant = {
  id: "grant-1",
  client_name: "Claude Code",
  scopes: ["mcp:servers.read", "mcp:actions.request"],
  servers: [{ id: "srv-1", name: "survival" }],
  created_at: new Date().toISOString(),
  expires_at: new Date(Date.now() + 86_400_000).toISOString(),
  revoked_at: null,
  last_used_at: null,
  last_used_ip: null,
  active: true,
  overrides: {
    require_password: null,
    auto_approve_lifecycle: null,
    auto_approve_upgrades: null,
  },
  policy: {
    require_password: true,
    auto_approve_lifecycle: false,
    auto_approve_upgrades: false,
  },
};

const securePolicy = {
  require_password: true,
  auto_approve_lifecycle: false,
  auto_approve_upgrades: false,
};

const pendingAction: MCPActionRequest = {
  id: "act-1",
  grant_id: "grant-1",
  client_name: "Claude Code",
  server_id: "srv-1",
  server_name: "survival",
  action: "restart",
  reason: "TPS has been under 5 for ten minutes.",
  untrusted: true,
  status: "pending",
  created_at: new Date().toISOString(),
  expires_at: new Date(Date.now() + 600_000).toISOString(),
  decided_at: null,
  executed_at: null,
  failure_reason: null,
  requires_password: true,
};

function renderCard() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <RemoteAgentsCard />
    </QueryClientProvider>,
  );
}

describe("RemoteAgentsCard", () => {
  beforeEach(() => {
    vi.spyOn(api.auth.mfa, "status").mockResolvedValue({ enabled: false });
    vi.spyOn(api.mcp, "connection").mockResolvedValue(connection);
    vi.spyOn(api.mcp.grants, "list").mockResolvedValue([]);
    vi.spyOn(api.mcp.actionRequests, "list").mockResolvedValue([]);
    vi.spyOn(api.mcp.approvalSettings, "get").mockResolvedValue(securePolicy);
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  // The whole point of OAuth here is that connecting takes a browser click
  // rather than a pasted secret. A snippet carrying a token would defeat it.
  it("shows connect commands that contain no credential", async () => {
    renderCard();
    expect(
      await screen.findByText(/claude mcp add --transport http servermanager/),
    ).toBeInTheDocument();
    expect(screen.getByText(/\[mcp_servers.servermanager\]/)).toBeInTheDocument();

    const card = screen.getByText(/Remote agent connections/i).closest("div")
      ?.parentElement?.parentElement;
    expect(card?.textContent).not.toMatch(/mcsm_(pat|mcp[acr])_/);
    expect(card?.textContent).not.toMatch(/Bearer /);
  });

  it("says what to configure when the deployment has no public origin", async () => {
    vi.spyOn(api.mcp, "connection").mockResolvedValue({
      ...connection,
      configured: false,
      url: "",
      claude_code: "",
      codex_config: "",
      hermes_config: "",
    });
    renderCard();

    expect(await screen.findByText(/MCP_PUBLIC_ORIGIN/)).toBeInTheDocument();
    expect(screen.queryByText(/claude mcp add/)).not.toBeInTheDocument();
  });

  it("lists a connection with its servers and capabilities", async () => {
    vi.spyOn(api.mcp.grants, "list").mockResolvedValue([grant]);
    renderCard();

    expect(await screen.findByText("Claude Code")).toBeInTheDocument();
    expect(screen.getByText("survival")).toBeInTheDocument();
    expect(screen.getByText("Request lifecycle/version upgrades")).toBeInTheDocument();
    expect(screen.getByText("Active")).toBeInTheDocument();
  });

  it("confirms before revoking, then revokes without asking for a password", async () => {
    vi.spyOn(api.mcp.grants, "list").mockResolvedValue([grant]);
    const revoke = vi.spyOn(api.mcp.grants, "revoke").mockResolvedValue(undefined);
    renderCard();

    fireEvent.click(await screen.findByRole("button", { name: /^revoke$/i }));
    expect(revoke).not.toHaveBeenCalled();
    // Revocation only removes authority; a password prompt between an operator
    // and the stop button would make an incident worse.
    expect(screen.queryByLabelText(/confirm your password/i)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /revoke connection/i }));
    await waitFor(() => expect(revoke).toHaveBeenCalledWith("grant-1"));
  });

  it("shows the agent's reason as untrusted text", async () => {
    vi.spyOn(api.mcp.actionRequests, "list").mockResolvedValue([pendingAction]);
    renderCard();

    expect(
      await screen.findByText(/TPS has been under 5 for ten minutes\./),
    ).toBeInTheDocument();
    expect(screen.getByText(/untrusted text/i)).toBeInTheDocument();
  });


  it("shows the exact target and backup consequence for upgrade approval", async () => {
    vi.spyOn(api.mcp.actionRequests, "list").mockResolvedValue([
      { ...pendingAction, action: "upgrade:26.3", reason: "Compatibility plan reviewed." },
    ]);
    renderCard();

    expect(await screen.findByText(/Upgrade to 26\.3 survival/)).toBeInTheDocument();
    expect(screen.getByText(/creates one full restore-point backup/i)).toBeInTheDocument();
  });

  it("requires a password step-up before an action can run", async () => {
    vi.spyOn(api.mcp.actionRequests, "list").mockResolvedValue([pendingAction]);
    const approve = vi
      .spyOn(api.mcp.actionRequests, "approve")
      .mockResolvedValue({ ...pendingAction, status: "executed" });
    renderCard();

    fireEvent.click(await screen.findByRole("button", { name: /^approve$/i }));
    expect(approve).not.toHaveBeenCalled();

    const submit = screen.getByRole("button", { name: /approve and run/i });
    expect(submit).toBeDisabled();

    fireEvent.change(screen.getByLabelText(/confirm your password/i), {
      target: { value: "hunter2hunter2" },
    });
    expect(submit).toBeEnabled();
    fireEvent.click(submit);

    await waitFor(() =>
      expect(approve).toHaveBeenCalledWith("act-1", {
        password: "hunter2hunter2",
        totp_code: undefined,
      }),
    );
  });

  it("also demands a TOTP code when the account has MFA on", async () => {
    vi.spyOn(api.auth.mfa, "status").mockResolvedValue({ enabled: true });
    vi.spyOn(api.mcp.actionRequests, "list").mockResolvedValue([pendingAction]);
    renderCard();

    fireEvent.click(await screen.findByRole("button", { name: /^approve$/i }));
    fireEvent.change(screen.getByLabelText(/confirm your password/i), {
      target: { value: "hunter2hunter2" },
    });
    expect(screen.getByRole("button", { name: /approve and run/i })).toBeDisabled();

    fireEvent.change(screen.getByLabelText(/authentication code/i), {
      target: { value: "123456" },
    });
    expect(screen.getByRole("button", { name: /approve and run/i })).toBeEnabled();
  });

  it("refuses an action without a step-up", async () => {
    vi.spyOn(api.mcp.actionRequests, "list").mockResolvedValue([pendingAction]);
    const deny = vi
      .spyOn(api.mcp.actionRequests, "deny")
      .mockResolvedValue({ ...pendingAction, status: "denied" });
    renderCard();

    fireEvent.click(await screen.findByRole("button", { name: /^refuse$/i }));
    await waitFor(() => expect(deny).toHaveBeenCalledWith("act-1"));
  });

  // The step-up is a policy decision, so when the owner has turned it off the
  // approve button has to be the whole interaction rather than a form.
  it("approves in one click when the policy needs no password", async () => {
    vi.spyOn(api.mcp.actionRequests, "list").mockResolvedValue([
      { ...pendingAction, requires_password: false },
    ]);
    const approve = vi
      .spyOn(api.mcp.actionRequests, "approve")
      .mockResolvedValue({ ...pendingAction, status: "executed" });
    renderCard();

    fireEvent.click(await screen.findByRole("button", { name: /^approve$/i }));

    // No form appears, and nothing that could be mistaken for one.
    expect(screen.queryByLabelText(/confirm your password/i)).not.toBeInTheDocument();
    // Crucially, no empty password is sent: the API counts one as a failed
    // attempt, and spending throttle slots would lock the owner out.
    await waitFor(() => expect(approve).toHaveBeenCalledWith("act-1", undefined));
  });

  it("lets a connection override the account default", async () => {
    vi.spyOn(api.mcp.grants, "list").mockResolvedValue([grant]);
    const setApproval = vi
      .spyOn(api.mcp.grants, "setApproval")
      .mockResolvedValue(securePolicy);
    renderCard();

    fireEvent.click(await screen.findByRole("button", { name: /approvals/i }));
    fireEvent.change(screen.getByLabelText(/start \/ stop \/ restart/i), {
      target: { value: "off" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    // Tightening, so it goes through with no credentials attached.
    await waitFor(() =>
      expect(setApproval).toHaveBeenCalledWith("grant-1", {
        require_password: null,
        auto_approve_lifecycle: false,
        auto_approve_upgrades: null,
        password: undefined,
        totp_code: undefined,
      }),
    );
  });

  // The control that protects every approval has to protect itself: turning it
  // off is the first thing someone on a stolen session would do.
  it("demands a step-up before the approval gate can be relaxed", async () => {
    const save = vi
      .spyOn(api.mcp.approvalSettings, "set")
      .mockResolvedValue({ ...securePolicy, require_password: false });
    renderCard();

    // The toggles render from the secure default before the query resolves, so
    // wait for the loaded value before touching anything.
    const gate = await screen.findByLabelText(/ask for my password before approving/i);
    await waitFor(() => expect(gate).toBeChecked());

    fireEvent.click(gate);
    expect(screen.getByRole("button", { name: /^save$/i })).toBeDisabled();
    expect(screen.getByText(/removing a protection/i)).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText(/confirm your password/i), {
      target: { value: "hunter2hunter2" },
    });
    const submit = screen.getByRole("button", { name: /^save$/i });
    expect(submit).toBeEnabled();
    fireEvent.click(submit);

    await waitFor(() =>
      expect(save).toHaveBeenCalledWith({
        require_password: false,
        auto_approve_lifecycle: false,
        auto_approve_upgrades: false,
        password: "hunter2hunter2",
        totp_code: undefined,
      }),
    );
  });

  it("saves a tightening change without asking for anything", async () => {
    vi.spyOn(api.mcp.approvalSettings, "get").mockResolvedValue({
      require_password: false,
      auto_approve_lifecycle: true,
      auto_approve_upgrades: false,
    });
    const save = vi
      .spyOn(api.mcp.approvalSettings, "set")
      .mockResolvedValue(securePolicy);
    renderCard();

    const lifecycle = await screen.findByLabelText(
      /approve start, stop, and restart automatically/i,
    );
    await waitFor(() => expect(lifecycle).toBeChecked());

    fireEvent.click(lifecycle);
    expect(screen.queryByLabelText(/confirm your password/i)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await waitFor(() =>
      expect(save).toHaveBeenCalledWith({
        require_password: false,
        auto_approve_lifecycle: false,
        auto_approve_upgrades: false,
        password: undefined,
        totp_code: undefined,
      }),
    );
  });

  // The upgrade toggle is the one whose cost is least obvious from its label.
  it("warns harder about auto-approving upgrades than restarts", async () => {
    renderCard();

    fireEvent.click(
      await screen.findByLabelText(/approve start, stop, and restart automatically/i),
    );
    expect(screen.getByText(/restart during a session you cannot see/i)).toBeInTheDocument();

    fireEvent.click(
      screen.getByLabelText(/approve version upgrades automatically/i),
    );
    expect(
      screen.getByText(/rolling the world back to a restore point/i),
    ).toBeInTheDocument();
  });

  it("offers no approve button for a settled request", async () => {
    vi.spyOn(api.mcp.actionRequests, "list").mockResolvedValue([
      {
        ...pendingAction,
        status: "executed",
        decided_at: new Date().toISOString(),
        executed_at: new Date().toISOString(),
      },
    ]);
    renderCard();

    expect(await screen.findByText("Ran")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^approve$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^refuse$/i })).not.toBeInTheDocument();
  });
});
