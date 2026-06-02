import type {
  AuditEntry,
  Backup,
  BackupTarget,
  FileListing,
  InstalledMod,
  LoginResponse,
  ModSearchParams,
  ModSearchResult,
  ModUpdate,
  ModVersion,
  ModrinthProject,
  Node,
  Player,
  ScheduledTask,
  Server,
  TokenResponse,
  User,
} from "./types";

const BASE = "/api/v1";

function getToken(): string | null {
  return localStorage.getItem("access_token");
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  signal?: AbortSignal,
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
  });

  if (res.status === 401) {
    // Attempt token refresh
    const refreshToken = localStorage.getItem("refresh_token");
    if (refreshToken) {
      const refreshRes = await fetch(`${BASE}/auth/refresh`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: refreshToken }),
      });
      if (refreshRes.ok) {
        const { access_token } = (await refreshRes.json()) as TokenResponse;
        localStorage.setItem("access_token", access_token);
        // Retry original request
        return request<T>(method, path, body, signal);
      }
    }
    localStorage.removeItem("access_token");
    localStorage.removeItem("refresh_token");
    window.location.href = "/login";
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
): Promise<string> {
  const token = getToken();
  const headers: Record<string, string> = {};
  if (token) headers["Authorization"] = `Bearer ${token}`;
  if (body !== undefined) headers["Content-Type"] = "text/plain; charset=utf-8";

  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body,
  });

  if (res.status === 401) {
    const refreshToken = localStorage.getItem("refresh_token");
    if (refreshToken) {
      const refreshRes = await fetch(`${BASE}/auth/refresh`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: refreshToken }),
      });
      if (refreshRes.ok) {
        const { access_token } = (await refreshRes.json()) as TokenResponse;
        localStorage.setItem("access_token", access_token);
        return requestText(method, path, body);
      }
    }
    localStorage.removeItem("access_token");
    localStorage.removeItem("refresh_token");
    window.location.href = "/login";
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
    logout: (refreshToken: string) =>
      post("/auth/logout", { refresh_token: refreshToken }),
    refresh: (refreshToken: string) =>
      post<TokenResponse>("/auth/refresh", { refresh_token: refreshToken }),
    me: () => get<User>("/auth/me"),
  },

  servers: {
    list: (signal?: AbortSignal) => get<Server[]>("/servers", signal),
    get: (id: string) => get<Server>(`/servers/${id}`),
    create: (data: Partial<Server>) => post<Server>("/servers", data),
    update: (id: string, data: Partial<Server>) =>
      put<Server>(`/servers/${id}`, data),
    delete: (id: string) => del(`/servers/${id}`),
    start: (id: string) => post(`/servers/${id}/start`),
    stop: (id: string, graceful = true) =>
      post(`/servers/${id}/stop`, { graceful, timeout_sec: 30 }),
    restart: (id: string) => post(`/servers/${id}/restart`),
    kill: (id: string) => post(`/servers/${id}/kill`),
    status: (id: string) =>
      get<{ status: string; pid?: number }>(`/servers/${id}/status`),
    command: (id: string, command: string) =>
      post(`/servers/${id}/command`, { command }),
  },

  files: {
    list: (serverId: string, path: string) =>
      get<FileListing>(
        `/servers/${serverId}/files?path=${encodeURIComponent(path)}`,
      ),
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
    downloadUrl: (serverId: string, path: string) => {
      const token = getToken();
      const params = new URLSearchParams({ path });
      if (token) params.set("token", token);
      return `${BASE}/servers/${serverId}/files/download?${params.toString()}`;
    },
    upload: (serverId: string, dirPath: string, file: File) => {
      const token = getToken();
      const fd = new FormData();
      fd.append("files", file);
      return fetch(
        `${BASE}/servers/${serverId}/files/upload?path=${encodeURIComponent(dirPath)}`,
        {
          method: "POST",
          headers: token ? { Authorization: `Bearer ${token}` } : {},
          body: fd,
        },
      ).then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`);
      });
    },
  },

  players: {
    list: (serverId: string) => get<Player[]>(`/servers/${serverId}/players`),
  },

  mods: {
    list: (serverId: string) =>
      get<InstalledMod[]>(`/servers/${serverId}/mods`),
    search: (serverId: string, params: ModSearchParams) =>
      post<ModSearchResult>(`/servers/${serverId}/mods/search`, {
        query: params.query,
        loader: params.loader,
        mc_version: params.mcVersion,
        project_type: params.projectType,
        categories: params.categories,
        index: params.index,
        limit: params.limit ?? 20,
        offset: params.offset ?? 0,
      }),
    getVersions: (
      serverId: string,
      projectId: string,
      loader?: string,
      mcVersion?: string,
    ) =>
      get<ModVersion[]>(
        `/servers/${serverId}/mods/versions?project_id=${projectId}${loader ? `&loader=${loader}` : ""}${mcVersion ? `&mc_version=${mcVersion}` : ""}`,
      ),
    getProject: (serverId: string, projectId: string) =>
      get<ModrinthProject>(
        `/servers/${serverId}/mods/project?project_id=${encodeURIComponent(projectId)}`,
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
    updates: (serverId: string) =>
      get<ModUpdate[]>(`/servers/${serverId}/mods/updates`),
    update: (serverId: string, modId: string, versionId?: string) =>
      post(`/servers/${serverId}/mods/${modId}/update`, {
        version_id: versionId,
      }),
    pin: (serverId: string, modId: string, pinned: boolean) =>
      post(`/servers/${serverId}/mods/${modId}/pin`, { pinned }),
    uninstall: (serverId: string, modId: string) =>
      del(`/servers/${serverId}/mods/${modId}`),
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
    delete: (id: string) => del(`/users/${id}`),
  },
};
