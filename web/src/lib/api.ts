const TOKEN_KEY = "registry_token";

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}
export function setToken(t: string): void {
  localStorage.setItem(TOKEN_KEY, t);
}
export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY);
}

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

const enc = (s: string) => encodeURIComponent(s);

async function req(method: string, url: string, body?: unknown): Promise<any> {
  const headers: Record<string, string> = {};
  const token = getToken();
  if (token) headers["Authorization"] = "Bearer " + token;
  if (body !== undefined) headers["Content-Type"] = "application/json";

  const res = await fetch(url, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  if (res.status === 401) {
    clearToken();
    throw new ApiError("unauthorized", 401);
  }
  if (res.status === 204) return null;
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const j = await res.json();
      msg = j.error || j.message || msg;
    } catch {
      /* ignore */
    }
    throw new ApiError(msg, res.status);
  }
  const ct = res.headers.get("content-type") || "";
  return ct.includes("application/json") ? res.json() : res.text();
}

export interface MeResponse {
  user: {
    name: string;
    admin: boolean;
    groups: string[];
    anonymous: boolean;
    password_change_required?: boolean;
  };
}

export const api = {
  me: () => req("GET", "/api/v1/me") as Promise<MeResponse>,
  ssoProviders: () =>
    req("GET", "/auth/sso") as Promise<{ providers: { id: string; label: string; kind: string }[] }>,
  login: async (username: string, password: string) => {
    const basic = "Basic " + btoa(username + ":" + password);
    const res = await fetch("/auth/token", {
      method: "GET",
      headers: { Authorization: basic },
    });
    if (!res.ok) throw new ApiError("invalid credentials", res.status);
    const j = await res.json();
    setToken(j.token);
    return j;
  },

  // Registries (Nexus-style hosted / proxy / group)
  registries: () => req("GET", "/api/v1/registries"),
  adminRegistries: () => req("GET", "/api/v1/admin/registries"),
  adminRegistry: (name: string) =>
    req("GET", `/api/v1/admin/registries/${enc(name)}`),
  adminCreateRegistry: (b: any) => req("POST", "/api/v1/admin/registries", b),
  adminUpdateRegistry: (name: string, b: any) =>
    req("PUT", `/api/v1/admin/registries/${enc(name)}`, b),
  adminDeleteRegistry: (name: string) =>
    req("DELETE", `/api/v1/admin/registries/${enc(name)}`),
  adminWarmRegistry: (name: string, image: string) =>
    req("POST", `/api/v1/admin/registries/${enc(name)}/warm`, { image }),
  adminGC: (b: any) => req("POST", "/api/v1/admin/gc", b),
  aptKey: (name: string) => req("GET", `/api/v1/admin/registries/${enc(name)}/apt-key`),
  aptKeyCreate: (name: string) =>
    req("POST", `/api/v1/admin/registries/${enc(name)}/apt-key`, {}),
  aptKeyDelete: (name: string) =>
    req("DELETE", `/api/v1/admin/registries/${enc(name)}/apt-key`),

  // Single sign-on providers (browser login, UI-managed; secrets write-only).
  ssoPresets: () => req("GET", "/api/v1/admin/sso/presets"),
  ssoListProviders: () => req("GET", "/api/v1/admin/sso/providers"),
  ssoCreateProvider: (b: any) =>
    req("POST", "/api/v1/admin/sso/providers", b),
  ssoUpdateProvider: (id: string, b: any) =>
    req("PUT", `/api/v1/admin/sso/providers/${enc(id)}`, b),
  ssoDeleteProvider: (id: string) =>
    req("DELETE", `/api/v1/admin/sso/providers/${enc(id)}`),

  // User / browsing (registry-scoped)
  repos: (registry: string) =>
    req("GET", `/api/v1/repositories?registry=${enc(registry)}`),
  tags: (registry: string, repo: string) =>
    req("GET", `/api/v1/repositories/${enc(repo)}/tags?registry=${enc(registry)}`),
  manifest: (registry: string, repo: string, ref: string) =>
    req("GET", `/api/v1/repositories/${enc(repo)}/manifests/${enc(ref)}?registry=${enc(registry)}`),
  stats: (registry: string) =>
    req("GET", `/api/v1/stats?registry=${enc(registry)}`),

  // Global search + per-registry artifact browsing.
  browse: (registry: string, prefix?: string) =>
    req(
      "GET",
      `/api/v1/browse?registry=${enc(registry)}${prefix ? "&prefix=" + enc(prefix) : ""}`,
    ),
  search: (q: string, format?: string) =>
    req(
      "GET",
      `/api/v1/search?q=${enc(q)}${format ? "&format=" + enc(format) : ""}`,
    ),
  artifactUrl: (registry: string, path: string) =>
    `/api/v1/artifact?registry=${enc(registry)}&path=${enc(path)}`,
  changePassword: (current: string, next: string) =>
    req("POST", "/api/v1/account/password", {
      current_password: current,
      new_password: next,
    }),
  getSettings: () => req("GET", "/api/v1/admin/settings") as Promise<{ allow_anonymous: boolean }>,
  setSettings: (allowAnonymous: boolean) =>
    req("PUT", "/api/v1/admin/settings", { allow_anonymous: allowAnonymous }),

  // Admin
  adminUsers: () => req("GET", "/api/v1/admin/users"),
  adminCreateUser: (b: any) => req("POST", "/api/v1/admin/users", b),
  adminUpdateUser: (name: string, b: any) =>
    req("PUT", `/api/v1/admin/users/${enc(name)}`, b),
  adminDeleteUser: (name: string) =>
    req("DELETE", `/api/v1/admin/users/${enc(name)}`),
  userRoles: (name: string) =>
    req("GET", `/api/v1/admin/users/${enc(name)}/roles`),
  setUserRoles: (name: string, roles: string[]) =>
    req("PUT", `/api/v1/admin/users/${enc(name)}/roles`, { roles }),

  serviceAccounts: () => req("GET", "/api/v1/admin/service-accounts"),
  createServiceAccount: (b: any) =>
    req("POST", "/api/v1/admin/service-accounts", b),
  revokeServiceAccount: (key: string) =>
    req("DELETE", `/api/v1/admin/service-accounts/${enc(key)}`),

  // Self-service API keys: bound to the caller, optionally scoped.
  rolesCatalog: () => req("GET", "/api/v1/roles"),
  accountKeys: () => req("GET", "/api/v1/account/keys"),
  accountCreateKey: (b: any) =>
    req("POST", "/api/v1/account/keys", b),
  accountRevokeKey: (key: string) =>
    req("DELETE", `/api/v1/account/keys/${enc(key)}`),

  stores: () => req("GET", "/api/v1/admin/stores"),

  // Credentials. Values are write-only: they go in, and are referenced by name
  // from then on. There is no endpoint that reads one back.
  secrets: () => req("GET", "/api/v1/admin/secrets"),
  createSecret: (b: { key: string; value: string; description?: string; public?: Record<string, unknown> }) =>
    req("POST", "/api/v1/admin/secrets", b),
  updateSecret: (key: string, b: { value: string; description?: string; public?: Record<string, unknown> }) =>
    req("PUT", `/api/v1/admin/secrets/${enc(key)}`, b),
  deleteSecret: (key: string) => req("DELETE", `/api/v1/admin/secrets/${enc(key)}`),

  // Blob stores, referenced by registries.
  blobStores: () => req("GET", "/api/v1/admin/blob-stores"),
  blobStore: (name: string) => req("GET", `/api/v1/admin/blob-stores/${enc(name)}`),
  upsertBlobStore: (b: any, name?: string) =>
    req(name ? "PUT" : "POST", `/api/v1/admin/blob-stores${name ? "/" + enc(name) : ""}`, b),
  deleteBlobStore: (name: string) => req("DELETE", `/api/v1/admin/blob-stores/${enc(name)}`),

  // The permission vocabulary is declared by the server, so the UI offers a
  // real choice instead of a free-text field nobody can get right.
  permissions: () => req("GET", "/api/v1/admin/permissions"),

  roles: () => req("GET", "/api/v1/admin/roles"),
  upsertRole: (b: any, name?: string) =>
    req(name ? "PUT" : "POST", `/api/v1/admin/roles${name ? "/" + enc(name) : ""}`, b),
  deleteRole: (name: string) =>
    req("DELETE", `/api/v1/admin/roles/${enc(name)}`),

  groups: () => req("GET", "/api/v1/admin/groups"),
  upsertGroup: (b: any, name?: string) =>
    req(name ? "PUT" : "POST", `/api/v1/admin/groups${name ? "/" + enc(name) : ""}`, b),
  deleteGroup: (name: string) => req("DELETE", `/api/v1/admin/groups/${enc(name)}`),

  grants: () => req("GET", "/api/v1/admin/grants"),
  addGrant: (b: { subject: string; role: string; scope: string }) =>
    req("POST", "/api/v1/admin/grants", b),
  updateGrant: (id: number, b: { subject: string; role: string; scope: string }) =>
    req("PUT", `/api/v1/admin/grants/${id}`, b),
  deleteGrant: (id: number) => req("DELETE", `/api/v1/admin/grants/${id}`),

  // Anonymous identities: named callers with no credentials, recognised by
  // address, each carrying its own grants.
  anonymous: () => req("GET", "/api/v1/admin/anonymous"),
  upsertAnonymous: (b: any, name?: string) =>
    req(name ? "PUT" : "POST", `/api/v1/admin/anonymous${name ? "/" + enc(name) : ""}`, b),
  deleteAnonymous: (name: string) => req("DELETE", `/api/v1/admin/anonymous/${enc(name)}`),
};
