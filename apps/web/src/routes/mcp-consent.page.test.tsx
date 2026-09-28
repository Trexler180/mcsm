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
import type { MCPConsentView } from "@/lib/types";
import {
  ConsentPage,
  DIAGNOSTIC_SCOPES,
  OPERATOR_SCOPES,
  browserNav,
} from "./mcp-consent.page";

vi.mock("@/store/notifications", () => ({
  useNotifications: () => ({ success: vi.fn(), error: vi.fn() }),
}));

// The page reads its request id off the location, exactly as the API's
// /authorize redirect leaves it.
function withRequest(id: string | null) {
  const search = id === null ? "" : `?request=${encodeURIComponent(id)}`;
  window.history.replaceState({}, "", `/mcp-consent${search}`);
}

const view: MCPConsentView = {
  request_id: "req-1",
  client_name: "Claude Code",
  client_origin: "dynamic",
  redirect_host: "127.0.0.1:41234",
  resource: "https://panel.test/api/v1/mcp",
  scopes: [
    {
      scope: "mcp:servers.read",
      title: "See your servers",
      description: "List the servers you select below.",
      sensitive: false,
    },
    {
      scope: "mcp:actions.request",
      title: "Request start, stop, and restart",
      description: "File a request. Nothing happens until you approve it.",
      sensitive: true,
    },
  ],
  servers: [
    // Holds both requested scopes.
    { id: "srv-1", name: "survival", scopes: ["mcp:servers.read", "mcp:actions.request"] },
    // Read-only: the caller cannot back the action scope here.
    { id: "srv-2", name: "creative", scopes: ["mcp:servers.read"] },
  ],
  expires_at: new Date(Date.now() + 300_000).toISOString(),
  default_days: 30,
  max_days: 90,
  already_decided: false,
};

// A request for the full operator surface, used by the preset tests. Both the
// legacy approval-gated scope and the direct ones are offered, which is the
// case where confusing them would matter most.
const operatorView: MCPConsentView = {
  ...view,
  scopes: [
    ...DIAGNOSTIC_SCOPES.map((scope) => ({
      scope,
      title: `diag ${scope}`,
      description: "read-only",
      sensitive: false,
    })),
    {
      scope: "mcp:actions.request" as const,
      title: "Request start, stop, and restart",
      description: "File a request. Nothing happens until you approve it.",
      sensitive: true,
    },
    {
      scope: "mcp:backups.create" as const,
      title: "Request human-confirmed backups",
      description: "Every backup requires a separate human confirmation.",
      sensitive: true,
    },
    ...OPERATOR_SCOPES.map((scope) => ({
      scope,
      title: `op ${scope}`,
      description: "operator capability",
      sensitive: scope !== "mcp:mods.read",
    })),
  ],
  servers: [
    {
      id: "srv-1",
      name: "survival",
      scopes: [
        ...DIAGNOSTIC_SCOPES,
        "mcp:actions.request" as const,
        "mcp:backups.create" as const,
        ...OPERATOR_SCOPES,
      ],
    },
    { id: "srv-2", name: "creative", scopes: [...DIAGNOSTIC_SCOPES] },
  ],
};

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <ConsentPage />
    </QueryClientProvider>,
  );
}

describe("ConsentPage", () => {
  beforeEach(() => {
    withRequest("req-1");
    vi.spyOn(browserNav, "assign").mockImplementation(() => {});
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("names the client and says a self-registered name is only a claim", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(view);
    renderPage();

    expect(await screen.findByTestId("consent-client")).toHaveTextContent(
      "Claude Code",
    );
    expect(screen.getByText(/registered itself and chose its own name/i)).toBeInTheDocument();
    // The redirect host is the operator's own machine; showing it is how they
    // tell their own tool from someone else's.
    expect(screen.getByText("127.0.0.1:41234")).toBeInTheDocument();
  });

  // The safe default: a client can ask for mutation authority, but a human has
  // to add it. Approving the screen as it arrives can never grant more than a
  // read.
  it("starts with only the read-only capabilities ticked", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(view);
    renderPage();

    expect(await screen.findByRole("checkbox", { name: "See your servers" })).toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "Request start, stop, and restart" }),
    ).not.toBeChecked();
  });

  it("never pre-ticks a mutation capability, however broad the request", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(operatorView);
    renderPage();

    await screen.findByRole("checkbox", { name: "diag mcp:servers.read" });
    for (const scope of OPERATOR_SCOPES) {
      expect(
        screen.getByRole("checkbox", { name: `op ${scope}` }),
      ).not.toBeChecked();
    }
    expect(
      screen.getByRole("checkbox", { name: "Request start, stop, and restart" }),
    ).not.toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "Request human-confirmed backups" }),
    ).not.toBeChecked();
    for (const scope of DIAGNOSTIC_SCOPES) {
      expect(screen.getByRole("checkbox", { name: `diag ${scope}` })).toBeChecked();
    }
  });

  it("the Server operator preset grants routine operations but no backup or approval-request scope", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(operatorView);
    const decide = vi
      .spyOn(api.mcp.consent, "decide")
      .mockResolvedValue({ redirect_to: "http://127.0.0.1:41234/cb?code=x" });
    renderPage();

    fireEvent.click(await screen.findByRole("button", { name: /server operator/i }));

    for (const scope of OPERATOR_SCOPES) {
      await waitFor(() =>
        expect(screen.getByRole("checkbox", { name: `op ${scope}` })).toBeChecked(),
      );
    }
    for (const scope of DIAGNOSTIC_SCOPES) {
      expect(screen.getByRole("checkbox", { name: `diag ${scope}` })).toBeChecked();
    }
    // Both capabilities require a separate human decision, so routine
    // operations must never bundle either one.
    expect(
      screen.getByRole("checkbox", { name: "Request start, stop, and restart" }),
    ).not.toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "Request human-confirmed backups" }),
    ).not.toBeChecked();

    fireEvent.click(screen.getByRole("checkbox", { name: "survival" }));
    fireEvent.click(screen.getByRole("button", { name: /allow access/i }));
    await waitFor(() => expect(decide).toHaveBeenCalledTimes(1));
    expect(decide.mock.calls[0][0].scopes).not.toContain("mcp:actions.request");
    expect(decide.mock.calls[0][0].scopes).not.toContain("mcp:backups.create");
    expect(decide.mock.calls[0][0].scopes.sort()).toEqual(
      [...DIAGNOSTIC_SCOPES, ...OPERATOR_SCOPES].sort(),
    );
  });

  it("the Diagnostics only preset is a way back to read-only", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(operatorView);
    renderPage();

    fireEvent.click(await screen.findByRole("button", { name: /server operator/i }));
    await waitFor(() =>
      expect(
        screen.getByRole("checkbox", { name: "op mcp:power.stop" }),
      ).toBeChecked(),
    );

    fireEvent.click(screen.getByRole("button", { name: /diagnostics only/i }));
    for (const scope of OPERATOR_SCOPES) {
      await waitFor(() =>
        expect(
          screen.getByRole("checkbox", { name: `op ${scope}` }),
        ).not.toBeChecked(),
      );
    }
    for (const scope of DIAGNOSTIC_SCOPES) {
      expect(screen.getByRole("checkbox", { name: `diag ${scope}` })).toBeChecked();
    }
  });

  // A preset selects, it does not request. It can only ever tick capabilities
  // the client actually asked for.
  it("a preset cannot select a capability the client did not request", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(view);
    const decide = vi
      .spyOn(api.mcp.consent, "decide")
      .mockResolvedValue({ redirect_to: "http://127.0.0.1:41234/cb?code=x" });
    renderPage();

    // This request offers no operator scope at all, so the preset is inert.
    expect(await screen.findByRole("button", { name: /server operator/i })).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: /diagnostics only/i }));
    fireEvent.click(screen.getByRole("checkbox", { name: "survival" }));
    fireEvent.click(screen.getByRole("button", { name: /allow access/i }));

    await waitFor(() => expect(decide).toHaveBeenCalledTimes(1));
    expect(decide.mock.calls[0][0].scopes).toEqual(["mcp:servers.read"]);
  });

  // The warning is the only thing standing between a human and an agent that
  // stops their server without asking, so it must appear exactly when it applies.
  it("warns that operator mutations execute without another click", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(operatorView);
    renderPage();

    await screen.findByRole("button", { name: /server operator/i });
    expect(screen.queryByText(/run immediately/i)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("checkbox", { name: "op mcp:power.stop" }));
    expect(await screen.findByText(/run immediately/i)).toBeInTheDocument();
    expect(
      screen.getByText(/will not be asked again for each action/i),
    ).toBeInTheDocument();
  });

  // mods.read changes nothing, so it must not raise the execute-immediately
  // warning — a warning shown for a read would teach people to ignore it.
  it("does not warn for the read-only member of the operator group", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(operatorView);
    renderPage();

    fireEvent.click(await screen.findByRole("checkbox", { name: "op mcp:mods.read" }));
    await waitFor(() =>
      expect(screen.getByRole("checkbox", { name: "op mcp:mods.read" })).toBeChecked(),
    );
    expect(screen.queryByText(/run immediately/i)).not.toBeInTheDocument();
  });

  // The approval-gated scope must not borrow the direct path's warning, and
  // must keep saying that nothing runs without a human.
  it("keeps the approval-gated scope's notice distinct from the direct one", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(operatorView);
    renderPage();

    fireEvent.click(
      await screen.findByRole("checkbox", {
        name: "Request start, stop, and restart",
      }),
    );
    expect(
      await screen.findByText(/only files a request/i),
    ).toBeInTheDocument();
    expect(screen.queryByText(/run immediately/i)).not.toBeInTheDocument();
  });

  // The API re-checks every (server, scope) pair and refuses the whole request
  // if one fails. Offering a pair it would reject produces a confusing error,
  // so the screen has to mirror that intersection.
  it("disables a server the caller cannot back with every ticked capability", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(view);
    renderPage();

    // Read-only by default, so both servers are delegatable.
    expect(await screen.findByRole("checkbox", { name: "creative" })).toBeEnabled();
    expect(screen.getByRole("checkbox", { name: "survival" })).toBeEnabled();

    // Adding the action scope takes the read-only server out of reach.
    fireEvent.click(
      screen.getByRole("checkbox", { name: "Request start, stop, and restart" }),
    );
    await waitFor(() =>
      expect(screen.getByRole("checkbox", { name: "creative" })).toBeDisabled(),
    );
    expect(screen.getByRole("checkbox", { name: "survival" })).toBeEnabled();
  });

  it("drops an already-selected server when a new capability outranks it", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(view);
    const decide = vi
      .spyOn(api.mcp.consent, "decide")
      .mockResolvedValue({ redirect_to: "http://127.0.0.1:41234/cb?code=x" });
    renderPage();

    // Read-only by default: tick both servers, then add the action scope.
    fireEvent.click(await screen.findByRole("checkbox", { name: "survival" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "creative" }));
    fireEvent.click(
      screen.getByRole("checkbox", { name: "Request start, stop, and restart" }),
    );

    await waitFor(() =>
      expect(screen.getByRole("checkbox", { name: "creative" })).toBeDisabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: /allow access/i }));

    await waitFor(() => expect(decide).toHaveBeenCalledTimes(1));
    // Only the server that can back both scopes survives.
    expect(decide.mock.calls[0][0].server_ids).toEqual(["srv-1"]);
  });

  it("sends the narrowed decision and hands the browser to the API's redirect", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(view);
    const decide = vi
      .spyOn(api.mcp.consent, "decide")
      .mockResolvedValue({ redirect_to: "http://127.0.0.1:41234/cb?code=x" });
    renderPage();

    // The default selection is already read-only; just pick a server.
    fireEvent.click(await screen.findByRole("checkbox", { name: "survival" }));
    fireEvent.change(screen.getByLabelText(/access expires after/i), {
      target: { value: "7" },
    });
    fireEvent.click(screen.getByRole("button", { name: /allow access/i }));

    await waitFor(() => expect(decide).toHaveBeenCalledTimes(1));
    expect(decide.mock.calls[0][0]).toMatchObject({
      request_id: "req-1",
      approve: true,
      scopes: ["mcp:servers.read"],
      server_ids: ["srv-1"],
      days: 7,
    });
    expect(browserNav.assign).toHaveBeenCalledWith(
      "http://127.0.0.1:41234/cb?code=x",
    );
  });

  it("will not approve without a server, and refusing needs no selection", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(view);
    const decide = vi
      .spyOn(api.mcp.consent, "decide")
      .mockResolvedValue({ redirect_to: "http://127.0.0.1:41234/cb?error=access_denied" });
    renderPage();

    expect(await screen.findByRole("button", { name: /allow access/i })).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: /refuse/i }));
    await waitFor(() => expect(decide).toHaveBeenCalledTimes(1));
    expect(decide.mock.calls[0][0].approve).toBe(false);
  });

  it("refuses a duration beyond the maximum the API would accept", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(view);
    renderPage();

    fireEvent.click(await screen.findByRole("checkbox", { name: "survival" }));
    const allow = screen.getByRole("button", { name: /allow access/i });
    expect(allow).toBeEnabled();

    fireEvent.change(screen.getByLabelText(/access expires after/i), {
      target: { value: "91" },
    });
    expect(allow).toBeDisabled();
  });

  it("treats an expired, unknown, or already-decided request the same way", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue({
      ...view,
      already_decided: true,
    });
    renderPage();

    expect(
      await screen.findByText(/no longer valid/i),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /allow access/i })).not.toBeInTheDocument();
  });

  it("explains a link with no request instead of calling the API", async () => {
    withRequest(null);
    const get = vi.spyOn(api.mcp.consent, "get");
    renderPage();

    expect(
      await screen.findByText(/no connection request in this link/i),
    ).toBeInTheDocument();
    expect(get).not.toHaveBeenCalled();
  });

  it("never renders anything that looks like a token", async () => {
    vi.spyOn(api.mcp.consent, "get").mockResolvedValue(view);
    const { container } = render(
      <QueryClientProvider
        client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
      >
        <ConsentPage />
      </QueryClientProvider>,
    );
    await screen.findAllByTestId("consent-client");
    expect(container.textContent).not.toMatch(/mcsm_mcp[acr]_/);
  });
});
