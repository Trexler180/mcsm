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
import type { AccessKey, Server } from "@/lib/types";
import { AccessKeysCard } from "./access-keys";

vi.mock("@/store/notifications", () => ({
  useNotifications: () => ({ success: vi.fn(), error: vi.fn() }),
}));

const server = {
  id: "srv-1",
  name: "survival",
  permissions: ["view", "power.restart", "console"],
} as unknown as Server;

const existingKey: AccessKey = {
  id: "key-1",
  user_id: "user-1",
  name: "diagnosis agent",
  token_prefix: "mcsm_pat_AbCdEf12",
  scopes: ["view"],
  server_ids: ["srv-1"],
  expires_at: new Date(Date.now() + 86_400_000).toISOString(),
  created_at: new Date().toISOString(),
  last_used_at: null,
  last_used_ip: null,
  revoked_at: null,
};

function renderCard() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <AccessKeysCard />
    </QueryClientProvider>,
  );
}

/** Opens the create form and fills the fields the API requires. */
async function openAndFillForm(opts: { mfa?: boolean } = {}) {
  fireEvent.click(await screen.findByRole("button", { name: /new key/i }));
  fireEvent.change(screen.getByLabelText("Name"), {
    target: { value: "diagnosis agent" },
  });
  fireEvent.click(await screen.findByRole("checkbox", { name: "survival" }));
  fireEvent.change(screen.getByLabelText("Confirm your password"), {
    target: { value: "hunter2hunter2" },
  });
  if (opts.mfa) {
    fireEvent.change(screen.getByLabelText("Authentication code"), {
      target: { value: "123456" },
    });
  }
}

describe("AccessKeysCard", () => {
  beforeEach(() => {
    vi.spyOn(api.auth.mfa, "status").mockResolvedValue({ enabled: false });
    vi.spyOn(api.servers, "list").mockResolvedValue([server]);
    vi.spyOn(api.auth.apiKeys, "list").mockResolvedValue([]);
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("never offers the admin scope, which the API refuses on a key", async () => {
    renderCard();
    fireEvent.click(await screen.findByRole("button", { name: /new key/i }));
    expect(
      screen.queryByRole("checkbox", { name: "Manage access" }),
    ).not.toBeInTheDocument();
  });

  it("defaults to read-only diagnosis without silently granting file reads", async () => {
    renderCard();
    fireEvent.click(await screen.findByRole("button", { name: /new key/i }));
    // Checkboxes are named from the shared permission vocabulary, so "View"
    // (the server) and "Players · View saved data" stay distinguishable.
    expect(screen.getByRole("checkbox", { name: "View" })).toBeChecked();
    expect(
      screen.getByRole("checkbox", { name: "Files · Read / download" }),
    ).not.toBeChecked();
    expect(screen.getByRole("checkbox", { name: "Console" })).not.toBeChecked();
  });

  it("requires a name, a server, and a password before it will submit", async () => {
    renderCard();
    fireEvent.click(await screen.findByRole("button", { name: /new key/i }));
    const submit = screen.getByRole("button", { name: /create key/i });
    expect(submit).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Name"), {
      target: { value: "diagnosis agent" },
    });
    expect(submit).toBeDisabled();

    fireEvent.click(screen.getByRole("checkbox", { name: "survival" }));
    expect(submit).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Confirm your password"), {
      target: { value: "hunter2hunter2" },
    });
    expect(submit).toBeEnabled();
  });

  it("asks for a TOTP code when the account has MFA on", async () => {
    vi.spyOn(api.auth.mfa, "status").mockResolvedValue({ enabled: true });
    renderCard();
    await openAndFillForm();
    // Password alone is not enough once a second factor exists.
    expect(screen.getByRole("button", { name: /create key/i })).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Authentication code"), {
      target: { value: "123456" },
    });
    expect(screen.getByRole("button", { name: /create key/i })).toBeEnabled();
  });

  it("sends a bounded expiry and shows the secret exactly once", async () => {
    const create = vi
      .spyOn(api.auth.apiKeys, "create")
      .mockResolvedValue({ key: existingKey, token: "mcsm_pat_the-real-secret" });
    renderCard();
    await openAndFillForm();
    fireEvent.click(screen.getByRole("button", { name: /create key/i }));

    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    const body = create.mock.calls[0][0];
    expect(body.server_ids).toEqual(["srv-1"]);
    expect(body.scopes).toEqual(["view"]);
    expect(body.password).toBe("hunter2hunter2");
    const expiry = new Date(body.expires_at).getTime();
    expect(expiry).toBeGreaterThan(Date.now());
    expect(expiry).toBeLessThanOrEqual(Date.now() + 91 * 86_400_000);

    // The secret is displayed once...
    const panel = await screen.findByTestId("access-key-secret");
    expect(panel).toHaveTextContent("mcsm_pat_the-real-secret");

    // ...and dismissing it removes it from the DOM for good.
    fireEvent.click(screen.getByRole("button", { name: /i've saved it/i }));
    await waitFor(() =>
      expect(screen.queryByTestId("access-key-secret")).not.toBeInTheDocument(),
    );
    expect(screen.queryByText(/mcsm_pat_the-real-secret/)).not.toBeInTheDocument();
  });

  // A picked day is sent as its local end-of-day, which lands up to a day past
  // "today + N", while the API cap is an exact 90 x 24h from when the request
  // arrives. The picker therefore has to stop a day short, or its own maximum
  // would produce a timestamp the API refuses.
  it("never offers a date whose end-of-day exceeds the API's 90-day cap", async () => {
    renderCard();
    fireEvent.click(await screen.findByRole("button", { name: /new key/i }));
    const input = screen.getByLabelText("Expires") as HTMLInputElement;

    const latest = new Date(`${input.max}T23:59:59`).getTime();
    expect(latest).toBeLessThanOrEqual(Date.now() + 90 * 86_400_000);
    // ...and not so conservative that it silently loses a usable week.
    expect(latest).toBeGreaterThan(Date.now() + 88 * 86_400_000);

    const earliest = new Date(`${input.min}T23:59:59`).getTime();
    expect(earliest).toBeGreaterThan(Date.now());
    // The prose still states the real API maximum.
    expect(screen.getByText(/at most 90 days out/i)).toBeInTheDocument();
  });

  it("keeps a hand-picked maximum date inside the cap when it is submitted", async () => {
    const create = vi
      .spyOn(api.auth.apiKeys, "create")
      .mockResolvedValue({ key: existingKey, token: "mcsm_pat_the-real-secret" });
    renderCard();
    await openAndFillForm();
    const input = screen.getByLabelText("Expires") as HTMLInputElement;
    fireEvent.change(input, { target: { value: input.max } });
    fireEvent.click(screen.getByRole("button", { name: /create key/i }));

    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    const expiry = new Date(create.mock.calls[0][0].expires_at).getTime();
    expect(expiry).toBeGreaterThan(Date.now());
    expect(expiry).toBeLessThanOrEqual(Date.now() + 90 * 86_400_000);
  });

  it("lists a key by prefix and scope without ever showing a secret", async () => {
    vi.spyOn(api.auth.apiKeys, "list").mockResolvedValue([existingKey]);
    renderCard();
    expect(await screen.findByText("diagnosis agent")).toBeInTheDocument();
    expect(screen.getByText(/mcsm_pat_AbCdEf12/)).toBeInTheDocument();
    expect(screen.getByText("survival")).toBeInTheDocument();
    expect(screen.getByText("Active")).toBeInTheDocument();
  });

  it("confirms before revoking", async () => {
    vi.spyOn(api.auth.apiKeys, "list").mockResolvedValue([existingKey]);
    const revoke = vi.spyOn(api.auth.apiKeys, "revoke").mockResolvedValue(undefined);
    renderCard();

    fireEvent.click(await screen.findByRole("button", { name: /^revoke$/i }));
    expect(revoke).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: /revoke key/i }));
    await waitFor(() => expect(revoke).toHaveBeenCalledWith("key-1"));
  });

  it("shows a revoked key as revoked, with no way to rotate it", async () => {
    vi.spyOn(api.auth.apiKeys, "list").mockResolvedValue([
      { ...existingKey, revoked_at: new Date().toISOString() },
    ]);
    renderCard();
    expect(await screen.findByText("Revoked")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^rotate$/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^revoke$/i })).not.toBeInTheDocument();
  });

  // Rotation keeps the expiry, so rotating an expired key would mint a token
  // that is already dead — the API answers 409. Revoking one still makes sense,
  // so that button has to stay.
  it("offers revoke but not rotate for an expired key", async () => {
    vi.spyOn(api.auth.apiKeys, "list").mockResolvedValue([
      { ...existingKey, expires_at: new Date(Date.now() - 60_000).toISOString() },
    ]);
    renderCard();

    expect(await screen.findByText("Expired")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /^rotate$/i }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /^revoke$/i })).toBeInTheDocument();
  });

  it("requires reauthentication to rotate, and shows the new secret once", async () => {
    vi.spyOn(api.auth.apiKeys, "list").mockResolvedValue([existingKey]);
    const rotate = vi
      .spyOn(api.auth.apiKeys, "rotate")
      .mockResolvedValue({ key: existingKey, token: "mcsm_pat_rotated-secret" });
    renderCard();

    fireEvent.click(await screen.findByRole("button", { name: /^rotate$/i }));
    const submit = screen.getByRole("button", { name: /rotate key/i });
    expect(submit).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Confirm your password"), {
      target: { value: "hunter2hunter2" },
    });
    fireEvent.click(screen.getByRole("button", { name: /rotate key/i }));

    await waitFor(() =>
      expect(rotate).toHaveBeenCalledWith("key-1", {
        password: "hunter2hunter2",
        totp_code: undefined,
      }),
    );
    expect(await screen.findByTestId("access-key-secret")).toHaveTextContent(
      "mcsm_pat_rotated-secret",
    );
  });
});
