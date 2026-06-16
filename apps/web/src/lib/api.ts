import type {
  AuditEntry,
  Backup,
  BackupTarget,
  FileListing,
  FileTree,
  AgentStatus,
  GameVersion,
  GeyserInfo,
  InstalledMod,
  IntegrationMeta,
  LoginResponse,
  LogEvent,
  ModCategory,
  Overview,
  ServerConflict,
  ModSearchParams,
  ModSearchResult,
  ModUpdate,
  ModUpdateRun,
  ModVersion,
  SkippedModVersion,
  ModrinthProject,
  Node,
  Player,
  PlayerActionKind,
  PlayerDetail,
  ScheduledTask,
  Server,
  TokenResponse,
  User,
} from "./types";

const BASE = "/api/v1";

function getToken(): string | null {
  return localStorage.getItem("access_token");
}

function setAccessToken(token: string) {
  localStorage.setItem("access_token", token);
  localStorage.removeItem("refresh_token");
}

function clearStoredSession() {
  localStorage.removeItem("access_token");
  localStorage.removeItem("refresh_token");
}

let refreshPromise: Promise<string | null> | null = null;

async function refreshAccessToken(): Promise<string | null> {
  if (!refreshPromise) {
    refreshPromise = fetch(`${BASE}/auth/refresh`, {
      method: "POST",
      credentials: "same-origin",
    })
      .then(async (res) => {
        if (!res.ok) return null;
        const { access_token } = (await res.json()) as TokenResponse;
        if (!access_token) return null;
        setAccessToken(access_token);
        return access_token;
      })
      .finally(() => {
        refreshPromise = null;
      });
  }
  return refreshPromise;
}

function tokenExpiresSoon(token: string, leewaySeconds = 60): boolean {
  try {
    const [, payload] = token.split(".");
    if (!payload) return true;
    const normalized = payload
      .replace(/-/g, "+")
      .replace(/_/g, "/")
      .padEnd(Math.ceil(payload.length / 4) * 4, "=");
    const decoded = JSON.parse(window.atob(normalized)) as { exp?: number };
    if (!decoded.exp) return true;
    return decoded.exp * 1000 <= Date.now() + leewaySeconds * 1000;
  } catch {
    return true;
  }
}

async function ensureAccessToken(): Promise<string | null> {
  const token = getToken();
  if (token && !tokenExpiresSoon(token)) {
    return token;
  }
  return refreshAccessToken();
}

async function retryAfterRefresh<T>(
  retry: () => Promise<T>,
): Promise<T | null> {
  const token = await refreshAccessToken();
  if (!token) return null;
  return retry();
}

function redirectToLogin() {
  clearStoredSession();
  if (window.location.pathname !== "/login") {
    window.location.href = "/login";
  }
}

async function fetchWithAuth(
  input: RequestInfo | URL,
  init: RequestInit = {},
  retry = true,
): Promise<Response> {
  const headers = new Headers(init.headers);
  const token = getToken();
  if (token) headers.set("Authorization", `Bearer ${token}`);

  const res = await fetch(input, {
    ...init,
    headers,
    credentials: "same-origin",
  });

  if (res.status === 401 && retry) {
    const refreshed = await refreshAccessToken();
    if (refreshed) {
      return fetchWithAuth(input, init, false);
    }
    redirectToLogin();
  }

  return res;
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
  retry = true,
): Promise<T> {
  const token = getToken();
  const headers: Record<string, string> = {};
  if (token) headers["Authorization"] = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "application/json";

  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    signal,
    credentials: "same-origin",
  });

  if (res.status === 401 && retry && path !== "/auth/login") {
    const retried = await retryAfterRefresh(() =>
      request<T>(method, path, body, signal, false),
    );
    if (retried !== null) {
      return retried;
    }
    redirectToLogin();
    throw new Error("Unauthorized");
  }

  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    try {
      const err = await res.json();
      msg = err.error || msg;
    } catch {}
    throw new Error(msg);
  }

  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

async function requestText(
  method: string,
  path: string,
  body?: string,
  retry = true,
): Promise<string> {
  const token = getToken();
  const headers: Record<string, string> = {};
  if (token) headers["Authorization"] = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "text/plain; charset=utf-8";

  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body,
    credentials: "same-origin",
  });

  if (res.status === 401 && retry) {
    const retried = await retryAfterRefresh(() =>
      requestText(method, path, body, false),
    );
    if (retried !== null) {
      return retried;
    }
    redirectToLogin();
    throw new Error("Unauthorized");
  }

  if (!res.ok) {
    const msg = await res.text().catch(() => `HTTP ${res.status}`);
    let parsedError = "";
    try {
      const parsed = JSON.parse(msg);
      parsedError = parsed.error || "";
    } catch {
      // Response was plain text, keep it as-is below.
    }
    throw new Error(parsedError || msg || `HTTP ${res.status}`);
  }

  return res.text();
}

const get = <T>(path: string, signal?: AbortSignal) =>
  request<T>("GET", path, undefined, signal);
const post = <T>(path: string, body?: unknown) =>
  request<T>("POST", path, body);
const put = <T>(path: string, body?: unknown) => request<T>("PUT", path, body);
const del = <T>(path: string) => request<T>("DELETE", path);

export const api = {
  auth: {
    login: (email: string, password: string) =>
      post<LoginResponse>("/auth/login", { email, password }),
    logout: () => post("/auth/logout"),
    refresh: async () => {
      const accessToken = await refreshAccessToken();
      if (!accessToken) throw new Error("Unauthorized");
      return { access_token: accessToken };
    },
    ensureAccessToken,
    // Mint a short-lived, single-use ticket for requests that can't send an
    // Authorization header: file downloads (plain <a> navigations) and the
    // console/metrics WebSocket handshakes. Replaces putting the raw JWT in the
    // query string.
    ticket: () => post<{ ticket: string; expires_in: number }>("/auth/ticket"),
    me: () => get<User>("/auth/me"),
  },

  servers: {
    list: (signal?: AbortSignal) => get<Server[]>("/servers", signal),
    get: (id: string) => get<Server>(`/servers/${id}`),
    create: (data: Partial<Server>) => post<Server>("/servers", data),
    update: (id: string, data: Partial<Server>) =>
      put<Server>(`/servers/${id}`, data),
    delete: (id: string, opts?: { files?: boolean; backups?: boolean }) => {
      const q = new URLSearchParams();
      if (opts?.files) q.set("files", "true");
      if (opts?.backups) q.set("backups", "true");
      const qs = q.toString();
      return del(`/servers/${id}${qs ? `?${qs}` : ""}`);
    },
    start: (id: string) => post(`/servers/${id}/start`),
    stop: (id: string, graceful = true) =>
      post(`/servers/${id}/stop`, { graceful, timeout_sec: 30 }),
    restart: (id: string) => post(`/servers/${id}/restart`),
    reinstall: (id: string) => post(`/servers/${id}/reinstall`),
    kill: (id: string) => post(`/servers/${id}/kill`),
    status: (id: string) => get<AgentStatus>(`/servers/${id}/status`),
    command: (id: string, command: string) =>
      post(`/servers/${id}/command`, { command }),
    logEvents: (id: string, opts?: { level?: string; limit?: number }) => {
      const q = new URLSearchParams();
      if (opts?.level) q.set("level", opts.level);
      if (opts?.limit) q.set("limit", String(opts.limit));
      const qs = q.toString();
      return get<LogEvent[]>(`/servers/${id}/log-events${qs ? `?${qs}` : ""}`);
    },
  },

  overview: {
    get: (signal?: AbortSignal) => get<Overview>("/overview", signal),
  },

  resourcePacks: {
    publicPath: (serverId: string, publicId: string) =>
      `${BASE}/public/servers/${serverId}/resource-pack/${encodeURIComponent(publicId)}`,
    publicUrl: (serverId: string, publicId: string) =>
      `${window.location.origin}${api.resourcePacks.publicPath(serverId, publicId)}`,
  },

  files: {
    list: (serverId: string, path: string) =>
      get<FileListing>(
        `/servers/${serverId}/files?path=${encodeURIComponent(path)}`,
      ),
    // Recursively list every file beneath path in a single request. The agent
    // walks the tree locally, so this avoids one round-trip per directory.
    tree: (
      serverId: string,
      path: string,
      opts?: { depth?: number; max?: number },
    ) => {
      const params = new URLSearchParams({ path });
      if (opts?.depth) params.set("depth", String(opts.depth));
      if (opts?.max) params.set("max", String(opts.max));
      return get<FileTree>(
        `/servers/${serverId}/files/tree?${params.toString()}`,
      );
    },
    readContent: (serverId: string, path: string) =>
      requestText(
        "GET",
        `/servers/${serverId}/files/content?path=${encodeURIComponent(path)}`,
      ),
    writeContent: (serverId: string, path: string, content: string) =>
      requestText(
        "PUT",
        `/servers/${serverId}/files/content?path=${encodeURIComponent(path)}`,
        content,
      ),
    delete: (serverId: string, path: string) =>
      del(`/servers/${serverId}/files?path=${encodeURIComponent(path)}`),
    rename: (serverId: string, from: string, to: string) =>
      post(`/servers/${serverId}/files/rename`, { from, to }),
    mkdir: (serverId: string, path: string) =>
      post(`/servers/${serverId}/files/mkdir`, { path }),
    // Trigger a browser download. A plain <a href> can't carry an auth header,
    // so we mint a single-use ticket first and append it to the URL, then click
    // a synthetic link. The ticket is consumed by the request and expires in
    // seconds, so the URL is inert if it leaks into history/logs.
    download: async (serverId: string, path: string) => {
      const { ticket } = await api.auth.ticket();
      const params = new URLSearchParams({ path, ticket });
      const url = `${BASE}/servers/${serverId}/files/download?${params.toString()}`;
      const a = document.createElement("a");
      a.href = url;
      a.download = path.split("/").pop() ?? "download";
      document.body.appendChild(a);
      a.click();
      a.remove();
    },
    // Fetch the raw, untouched bytes of a file (binary-safe). The download
    // endpoint streams the file verbatim, unlike readContent which forces text.
    // This goes through fetchWithAuth, so it uses the Authorization header — no
    // ticket needed.
    readBytes: (serverId: string, path: string) => {
      const params = new URLSearchParams({ path });
      return fetchWithAuth(
        `${BASE}/servers/${serverId}/files/download?${params.toString()}`,
      ).then(async (r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
        return new Uint8Array(await r.arrayBuffer());
      });
    },
    // Overwrite a file with raw bytes by uploading into its parent directory
    // under the same name. WriteUpload uses os.Create, so it replaces in place.
    writeBytes: (serverId: string, path: string, bytes: Uint8Array) => {
      const slash = path.lastIndexOf("/");
      const dir = slash <= 0 ? "/" : path.slice(0, slash);
      const name = path.slice(slash + 1);
      const fd = new FormData();
      fd.append("files", new Blob([bytes as BlobPart]), name);
      return fetchWithAuth(
        `${BASE}/servers/${serverId}/files/upload?path=${encodeURIComponent(dir)}`,
        {
          method: "POST",
          body: fd,
        },
      ).then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
      });
    },
    upload: (serverId: string, dirPath: string, file: File) => {
      const fd = new FormData();
      fd.append("files", file);
      return fetchWithAuth(
        `${BASE}/servers/${serverId}/files/upload?path=${encodeURIComponent(dirPath)}`,
        {
          method: "POST",
          body: fd,
        },
      ).then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
      });
    },
  },

  players: {
    list: (serverId: string) => get<Player[]>(`/servers/${serverId}/players`),
    meta: (serverId: string) =>
      get<GeyserInfo>(`/servers/${serverId}/players/meta`),
    get: (serverId: string, uuid: string) =>
      get<PlayerDetail>(`/servers/${serverId}/players/${uuid}`),
    action: (
      serverId: string,
      body: {
        action: PlayerActionKind;
        name: string;
        uuid?: string;
        reason?: string;
      },
    ) => post(`/servers/${serverId}/players/action`, body),
  },

  mods: {
    list: (serverId: string) =>
      get<InstalledMod[]>(`/servers/${serverId}/mods`),
    sources: (serverId: string) =>
      get<Record<string, boolean>>(`/servers/${serverId}/mods/sources`),
    categories: (serverId: string, projectType?: string, source?: string) =>
      get<ModCategory[]>(
        `/servers/${serverId}/mods/categories?${new URLSearchParams({
          ...(projectType ? { project_type: projectType } : {}),
          ...(source ? { source } : {}),
        }).toString()}`,
      ),
    search: (serverId: string, params: ModSearchParams) =>
      post<ModSearchResult>(`/servers/${serverId}/mods/search`, {
        query: params.query,
        source: params.source,
        loader: params.loader,
        mc_version: params.mcVersion,
        project_type: params.projectType,
        categories: params.categories,
        index: params.index,
        environment: params.environment,
        limit: params.limit ?? 20,
        offset: params.offset ?? 0,
      }),
    getVersions: (
      serverId: string,
      projectId: string,
      loader?: string,
      mcVersion?: string,
      source?: string,
    ) =>
      get<ModVersion[]>(
        `/servers/${serverId}/mods/versions?project_id=${projectId}${loader ? `&loader=${loader}` : ""}${mcVersion ? `&mc_version=${mcVersion}` : ""}${source ? `&source=${source}` : ""}`,
      ),
    getVersion: (
      serverId: string,
      projectId: string,
      versionId: string,
      source?: string,
    ) =>
      get<ModVersion>(
        `/servers/${serverId}/mods/version?version_id=${encodeURIComponent(versionId)}&project_id=${encodeURIComponent(projectId)}${source ? `&source=${source}` : ""}`,
      ),
    getProject: (serverId: string, projectId: string, source?: string) =>
      get<ModrinthProject>(
        `/servers/${serverId}/mods/project?project_id=${encodeURIComponent(projectId)}${source ? `&source=${source}` : ""}`,
      ),
    install: (
      serverId: string,
      source: string,
      projectId: string,
      versionId: string,
      withDeps = true,
    ) =>
      post(`/servers/${serverId}/mods/install`, {
        source,
        project_id: projectId,
        version_id: versionId,
        with_deps: withDeps,
      }),
    installModpack: (serverId: string, projectId: string, versionId: string) =>
      post(`/servers/${serverId}/mods/install-modpack`, {
        project_id: projectId,
        version_id: versionId,
      }),
    uploadCustom: (serverId: string, files: File[]) => {
      const fd = new FormData();
      files.forEach((file) => fd.append("files", file));
      return fetchWithAuth(`${BASE}/servers/${serverId}/mods/upload`, {
        method: "POST",
        body: fd,
      }).then(async (r) => {
        if (!r.ok) {
          let msg = `HTTP ${r.status}`;
          try {
            const err = await r.json();
            msg = err.error || msg;
          } catch {}
          throw new Error(msg);
        }
        return r.json() as Promise<InstalledMod[]>;
      });
    },
    updates: (serverId: string) =>
      get<ModUpdate[]>(`/servers/${serverId}/mods/updates`),
    // Safe auto-update: apply updates, restart, watch boot health, revert and
    // blocklist anything that breaks the boot. Async; poll updateRun().
    autoUpdate: (serverId: string) =>
      post<ModUpdateRun>(`/servers/${serverId}/mods/auto-update`),
    updateRuns: (serverId: string, limit = 20) =>
      get<ModUpdateRun[]>(
        `/servers/${serverId}/mods/update-runs?limit=${limit}`,
      ),
    updateRun: (serverId: string, runId: string) =>
      get<ModUpdateRun>(`/servers/${serverId}/mods/update-runs/${runId}`),
    skippedVersions: (serverId: string) =>
      get<SkippedModVersion[]>(`/servers/${serverId}/mods/skipped-versions`),
    unskipVersion: (serverId: string, projectId: string, versionId: string) =>
      del(
        `/servers/${serverId}/mods/skipped-versions?project_id=${encodeURIComponent(projectId)}&version_id=${encodeURIComponent(versionId)}`,
      ),
    update: (serverId: string, modId: string, versionId?: string) =>
      post(`/servers/${serverId}/mods/${modId}/update`, {
        version_id: versionId,
      }),
    pin: (serverId: string, modId: string, pinned: boolean) =>
      post(`/servers/${serverId}/mods/${modId}/pin`, { pinned }),
    setEnabled: (serverId: string, modId: string, enabled: boolean) =>
      post<InstalledMod>(`/servers/${serverId}/mods/${modId}/enabled`, {
        enabled,
      }),
    uninstall: (serverId: string, modId: string) =>
      del(`/servers/${serverId}/mods/${modId}`),
    disableConflict: (serverId: string, modIds: string[]) =>
      post<{ disabled: string[] }>(
        `/servers/${serverId}/mods/disable-conflict`,
        { mod_ids: modIds },
      ),
    conflicts: (serverId: string, activeOnly = false) =>
      get<ServerConflict[]>(
        `/servers/${serverId}/mods/conflicts${activeOnly ? "?active=1" : ""}`,
      ),
    recordConflict: (
      serverId: string,
      body: { kind: string; summary: string; mods: string[] },
    ) => post<{ id: string }>(`/servers/${serverId}/mods/conflicts`, body),
  },

  minecraft: {
    versions: (platform: string, snapshots = false) =>
      get<GameVersion[]>(
        `/minecraft/versions?platform=${encodeURIComponent(platform)}${snapshots ? "&snapshots=true" : ""}`,
      ),
    loaders: (platform: string) =>
      get<GameVersion[]>(
        `/minecraft/loaders?platform=${encodeURIComponent(platform)}`,
      ),
  },

  audit: {
    list: (limit = 100) => get<AuditEntry[]>(`/audit?limit=${limit}`),
    forServer: (serverId: string, limit = 100) =>
      get<AuditEntry[]>(`/servers/${serverId}/audit?limit=${limit}`),
  },

  nodes: {
    list: (signal?: AbortSignal) => get<Node[]>("/nodes", signal),
    create: (data: Partial<Node> & { token: string }) =>
      post<Node>("/nodes", data),
    update: (id: string, data: Partial<Node>) =>
      put<Node>(`/nodes/${id}`, data),
    delete: (id: string) => del(`/nodes/${id}`),
  },

  backups: {
    list: (serverId: string) => get<Backup[]>(`/servers/${serverId}/backups`),
    create: (serverId: string) => post<Backup>(`/servers/${serverId}/backups`),
    restore: (serverId: string, backupId: string) =>
      post(`/servers/${serverId}/backups/${backupId}/restore`),
    delete: (serverId: string, backupId: string) =>
      del(`/servers/${serverId}/backups/${backupId}`),
    targets: {
      list: (serverId: string) =>
        get<BackupTarget[]>(`/servers/${serverId}/backup-targets`),
      create: (serverId: string, data: Partial<BackupTarget>) =>
        post<BackupTarget>(`/servers/${serverId}/backup-targets`, data),
    },
  },

  tasks: {
    list: (serverId: string) =>
      get<ScheduledTask[]>(`/servers/${serverId}/tasks`),
    create: (serverId: string, data: Partial<ScheduledTask>) =>
      post<ScheduledTask>(`/servers/${serverId}/tasks`, data),
    update: (serverId: string, taskId: string, data: Partial<ScheduledTask>) =>
      put<ScheduledTask>(`/servers/${serverId}/tasks/${taskId}`, data),
    delete: (serverId: string, taskId: string) =>
      del(`/servers/${serverId}/tasks/${taskId}`),
  },

  users: {
    list: () => get<User[]>("/users"),
    create: (email: string, password: string, role = "user") =>
      post<User>("/users", { email, password, role }),
    update: (
      id: string,
      data: { display_name?: string | null; role?: string; password?: string },
    ) => put<User>(`/users/${id}`, data),
    delete: (id: string) => del(`/users/${id}`),
  },

  settings: {
    integrations: {
      list: () => get<IntegrationMeta[]>("/settings/integrations"),
      set: (key: string, value: string) =>
        put(`/settings/integrations/${key}`, { value }),
      remove: (key: string) => del(`/settings/integrations/${key}`),
    },
  },
};
