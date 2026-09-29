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
};

export type FTPUser = {
  id: number;
  username: string;
  root_folder: string;
  enabled: boolean;
  ftp_enabled: boolean;
  sftp_enabled: boolean;
  created_at: string;
  last_login: string | null;
};

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
