export interface Entry { dn: string; attrs: Record<string, string[]> }
export interface Change { op: "add" | "replace" | "delete"; attr: string; values: string[] }
export interface ConnectConfig {
  url: string; bindDN: string; password: string; baseDN: string;
  startTLS: boolean; skipVerify: boolean;
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(path, { ...init, headers: { "Content-Type": "application/json" } });
  if (r.status === 204) return undefined as T;
  const body = await r.json();
  if (!r.ok) throw new Error(body.error ?? r.statusText);
  return body as T;
}
const q = (dn: string) => `dn=${encodeURIComponent(dn)}`;

export const api = {
  connect: (c: ConnectConfig) => req<{ baseDN: string }>("/api/connect", { method: "POST", body: JSON.stringify(c) }),
  disconnect: () => req<void>("/api/disconnect", { method: "POST" }),
  session: () => req<{ baseDN: string }>("/api/session"),
  children: (dn: string) => req<Entry[]>(`/api/children?${q(dn)}`),
  get: (dn: string) => req<Entry>(`/api/entry?${q(dn)}`),
  search: (base: string, filter: string) =>
    req<Entry[]>(`/api/search?base=${encodeURIComponent(base)}&filter=${encodeURIComponent(filter)}`),
  add: (e: Entry) => req("/api/entry", { method: "POST", body: JSON.stringify(e) }),
  modify: (dn: string, c: Change[]) => req(`/api/entry?${q(dn)}`, { method: "PATCH", body: JSON.stringify(c) }),
  remove: (dn: string) => req(`/api/entry?${q(dn)}`, { method: "DELETE" }),
};
