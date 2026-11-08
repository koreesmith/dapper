export interface Entry { dn: string; attrs: Record<string, string[]> }
export interface Change { op: "add" | "replace" | "delete"; attr: string; values: string[] }
export interface User {
  dn: string; username: string; name: string; email: string; phones: string[] | null;
  title?: string; department?: string; suspended: boolean;
}
export interface UserPage { users: User[]; total: number; page: number; pageSize: number; truncated: boolean }
export interface UserQuery { q: string; sort: string; dir: "asc" | "desc"; page: number; pageSize: number }

export interface Me { dn: string; name: string; isAdmin: boolean }

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(path, { ...init, headers: { "Content-Type": "application/json", "X-Requested-With": "dapper" } });
  if (r.status === 204) return undefined as T;
  const body = await r.json();
  if (!r.ok) throw new Error(body.error ?? r.statusText);
  return body as T;
}
const q = (dn: string) => `dn=${encodeURIComponent(dn)}`;

export const api = {
  login: (username: string, password: string) =>
    req<Me>("/api/login", { method: "POST", body: JSON.stringify({ username, password }) }),
  logout: () => req<void>("/api/logout", { method: "POST" }),
  me: () => req<Me>("/api/me"),
  users: (p: UserQuery) =>
    req<UserPage>(`/api/admin/users?q=${encodeURIComponent(p.q)}&sort=${p.sort}&dir=${p.dir}&page=${p.page}&pageSize=${p.pageSize}`),
  children: (dn?: string) => req<Entry[]>(`/api/admin/children?${dn ? q(dn) : ""}`),
  get: (dn: string) => req<Entry>(`/api/admin/entry?${q(dn)}`),
  search: (base: string, filter: string) =>
    req<Entry[]>(`/api/admin/search?base=${encodeURIComponent(base)}&filter=${encodeURIComponent(filter)}`),
  add: (e: Entry) => req("/api/admin/entry", { method: "POST", body: JSON.stringify(e) }),
  modify: (dn: string, c: Change[]) => req(`/api/admin/entry?${q(dn)}`, { method: "PATCH", body: JSON.stringify(c) }),
  remove: (dn: string) => req(`/api/admin/entry?${q(dn)}`, { method: "DELETE" }),
};
