export interface Entry { dn: string; attrs: Record<string, string[]> }
export interface Change { op: "add" | "replace" | "delete"; attr: string; values: string[] }

export interface Field {
  attr: string; label: string; type: "text" | "email" | "tel" | "textarea"; multi: boolean;
  required?: boolean; userVisible: boolean; userEditable: boolean;
}
export interface FieldValue extends Field { values: string[] }
export type Attrs = Record<string, string[]>;

export interface Features { groups: boolean; suspendByMove: boolean; passwordMinLength: number }
export interface Me { dn: string; name: string; isAdmin: boolean; features: Features }

export interface User {
  dn: string; username: string; name: string; email: string; phones: string[] | null;
  title?: string; department?: string; suspended: boolean;
}
export interface UserPage { users: User[]; total: number; page: number; pageSize: number; truncated: boolean }
export interface UserQuery { q: string; sort: string; dir: "asc" | "desc"; status: string; page: number; pageSize: number }

export interface PersonDetail {
  dn: string; username: string; name: string; suspended: boolean; isSelf: boolean;
  fields: FieldValue[]; groups: { dn: string; name: string }[];
}
export interface Profile { dn: string; username: string; name: string; fields: FieldValue[]; groups: string[] }
export interface GroupSummary { dn: string; name: string; members: number; admin?: boolean }
export interface GroupDetail { dn: string; name: string; members: { dn: string; username: string; name: string }[] }
export interface Settings {
  connection: {
    ok: boolean; message: string; url: string; startTLS: boolean; bindDN: string; usersBaseDN: string;
    groupsBaseDN: string; suspendedBaseDN: string; adminGroupDN: string;
  };
  fields: Field[];
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(path, { ...init, headers: { "Content-Type": "application/json", "X-Requested-With": "dapper" } });
  if (r.status === 204) return undefined as T;
  let body: { error?: string } = {};
  try { body = await r.json(); } catch { /* non-JSON error page */ }
  if (!r.ok) throw new ApiError(body.error ?? "Something went wrong. Please try again.", r.status);
  return body as T;
}

export class ApiError extends Error {
  constructor(message: string, public status: number) { super(message); }
}

const q = (dn: string) => `dn=${encodeURIComponent(dn)}`;
const json = (method: string, body: unknown) => ({ method, body: JSON.stringify(body) });

export const api = {
  login: (username: string, password: string) => req<Me>("/api/login", json("POST", { username, password })),
  logout: () => req<void>("/api/logout", { method: "POST" }),
  me: () => req<Me>("/api/me"),

  // self-service
  profile: () => req<Profile>("/api/me/profile"),
  updateProfile: (attrs: Attrs) => req<{ changed: number }>("/api/me/profile", json("PATCH", { attrs })),
  changePassword: (current: string, next: string) => req("/api/me/password", json("POST", { current, new: next })),

  // people
  users: (p: UserQuery) =>
    req<UserPage>(`/api/admin/users?q=${encodeURIComponent(p.q)}&sort=${p.sort}&dir=${p.dir}&status=${p.status}&page=${p.page}&pageSize=${p.pageSize}`),
  person: (dn: string) => req<PersonDetail>(`/api/admin/user?${q(dn)}`),
  createPerson: (b: { username: string; attrs: Attrs; password?: string; groups: string[] }) =>
    req<{ dn: string; username: string; temporaryPassword?: string }>("/api/admin/users", json("POST", b)),
  updatePerson: (dn: string, attrs: Attrs) => req<{ changed: number }>(`/api/admin/user?${q(dn)}`, json("PATCH", { attrs })),
  suspend: (dn: string) => req<{ dn: string }>(`/api/admin/user/suspend?${q(dn)}`, { method: "POST" }),
  restore: (dn: string) => req<{ dn: string }>(`/api/admin/user/restore?${q(dn)}`, { method: "POST" }),
  deletePerson: (dn: string) => req(`/api/admin/user?${q(dn)}`, { method: "DELETE" }),
  resetPassword: (dn: string, password?: string) =>
    req<{ temporaryPassword?: string }>(`/api/admin/user/password?${q(dn)}`, json("POST", { password: password ?? "" })),

  // groups
  groups: () => req<GroupSummary[]>("/api/admin/groups"),
  group: (dn: string) => req<GroupDetail>(`/api/admin/group?${q(dn)}`),
  createGroup: (name: string) => req<{ dn: string }>("/api/admin/groups", json("POST", { name })),
  deleteGroup: (dn: string) => req(`/api/admin/group?${q(dn)}`, { method: "DELETE" }),
  addMember: (group: string, member: string) => req(`/api/admin/group/members?${q(group)}`, json("POST", { member })),
  removeMember: (group: string, member: string) =>
    req(`/api/admin/group/members?${q(group)}&member=${encodeURIComponent(member)}`, { method: "DELETE" }),

  // settings
  settings: () => req<Settings>("/api/admin/settings"),
  saveFieldPolicy: (fields: Field[]) => req<{ fields: Field[] }>("/api/admin/settings/fields", json("PUT", { fields })),

  // advanced (raw directory)
  children: (dn?: string) => req<Entry[]>(`/api/admin/children?${dn ? q(dn) : ""}`),
  get: (dn: string) => req<Entry>(`/api/admin/entry?${q(dn)}`),
  search: (base: string, filter: string) =>
    req<Entry[]>(`/api/admin/search?base=${encodeURIComponent(base)}&filter=${encodeURIComponent(filter)}`),
  modify: (dn: string, c: Change[]) => req(`/api/admin/entry?${q(dn)}`, json("PATCH", c)),
  remove: (dn: string) => req(`/api/admin/entry?${q(dn)}`, { method: "DELETE" }),
};
