// Tiny fetch helper. All requests carry cookies (same-origin admin API).
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    ...init,
    credentials: "include",
    headers: { "Content-Type": "application/json", ...(init.headers ?? {}) },
  });
  if (!res.ok) {
    const text = (await res.text().catch(() => "")).trim();
    throw new Error(text || `${res.status} ${res.statusText}`);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export type Session = {
  username: string;
  role: string;
  email: string;
  first_name: string;
  last_name: string;
  ftp_enabled: boolean;
  ftp_address: string;
  ftp_public_address: string;
  sftp_enabled: boolean;
  sftp_address: string;
  sftp_public_address: string;
  cos_address: string;
  /** Storage backends users can be assigned to ("cos", "local"). */
  storage_backends: string[];
  default_storage_backend: string;
};

export const STORAGE_BACKEND_LABELS: Record<string, string> = {
  cos: "Tencent COS",
  local: "Local disk",
};

export function storageBackendLabel(name: string | undefined): string {
  if (!name) return "—";
  return STORAGE_BACKEND_LABELS[name] ?? name;
}

export function getSession() {
  return api<Session>("/api/v1/auth/session");
}

export type FTPUser = {
  id: number;
  username: string;
  root_folder: string;
  enabled: boolean;
  ftp_enabled: boolean;
  sftp_enabled: boolean;
  /** Effective storage backend, fixed at creation. */
  storage_backend: string;
  created_at: string;
  last_login: string | null;
};

/** Fetches every (non-deleted) FTP user, paging past the API's 200 cap. */
export async function listAllUsers(): Promise<FTPUser[]> {
  const pageSize = 200;
  const out: FTPUser[] = [];
  for (let offset = 0; ; offset += pageSize) {
    const page = await api<FTPUser[]>(`/api/v1/users?limit=${pageSize}&offset=${offset}`);
    out.push(...page);
    if (page.length < pageSize) return out;
  }
}

export type AuditEvent = {
  event_time: string;
  username: string;
  client_ip: string;
  action: string;
  event_type: string;
  connection_type: string;
  backend_location: string;
  root_folder: string;
  path: string;
  success: boolean;
};

export const OIDC_AUTHORIZE_URL = "/api/v1/auth/oidc/authorize";

export function createUser(payload: {
  username: string;
  root_folder: string;
  password: string;
  enabled: boolean;
  ftp_enabled: boolean;
  sftp_enabled: boolean;
  storage_backend?: string;
}) {
  return api<FTPUser>("/api/v1/users", { method: "POST", body: JSON.stringify(payload) });
}

export function updateUser(
  username: string,
  payload: { root_folder: string; enabled: boolean; ftp_enabled: boolean; sftp_enabled: boolean },
) {
  return api<FTPUser>(`/api/v1/users/${encodeURIComponent(username)}`, { method: "PATCH", body: JSON.stringify(payload) });
}

export function resetUserPassword(username: string, password: string) {
  return api<void>(`/api/v1/users/${encodeURIComponent(username)}/password`, {
    method: "POST",
    body: JSON.stringify({ password }),
  });
}

export function setUserSFTPKey(username: string, publicKey: string) {
  return api<void>(`/api/v1/users/${encodeURIComponent(username)}/sftp-key`, {
    method: "POST",
    body: JSON.stringify({ public_key: publicKey }),
  });
}

export function deleteUser(username: string) {
  return api<void>(`/api/v1/users/${encodeURIComponent(username)}`, { method: "DELETE" });
}

export type FolderSuggestion = { name: string };

export function listFolders(prefix?: string) {
  const q = prefix ? `?prefix=${encodeURIComponent(prefix)}` : "";
  return api<FolderSuggestion[]>(`/api/v1/folders${q}`);
}

export type FileEntry = {
  name: string;
  isDir: boolean;
  size: number;
  modified: string;
};

export type ListFilesResponse = {
  prefix: string;
  entries: FileEntry[];
};

export function listFiles(userId: string | number, prefix = ""): Promise<ListFilesResponse> {
  const q = prefix ? `?prefix=${encodeURIComponent(prefix)}` : "";
  return api<ListFilesResponse>(`/api/v1/files/${userId}${q}`);
}

export type AdminUser = {
  id: number;
  username: string;
  role: string;
  created_at: string;
};

export function listAdmins() {
  return api<AdminUser[]>("/api/v1/admins");
}

export function createAdmin(payload: { username: string; password: string; role: string }) {
  return api<AdminUser>("/api/v1/admins", { method: "POST", body: JSON.stringify(payload) });
}

export function updateAdminRole(username: string, role: string) {
  return api<AdminUser>("/api/v1/admins/" + encodeURIComponent(username), {
    method: "PATCH",
    body: JSON.stringify({ role }),
  });
}

export function resetAdminPassword(username: string, password: string) {
  return api<void>("/api/v1/admins/" + encodeURIComponent(username) + "/password", {
    method: "POST",
    body: JSON.stringify({ password }),
  });
}

export function deleteAdmin(username: string) {
  return api<void>("/api/v1/admins/" + encodeURIComponent(username), { method: "DELETE" });
}
