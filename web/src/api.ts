export interface Entry { dn: string; attrs: Record<string, string[]> }
export interface Change { op: "add" | "replace" | "delete"; attr: string; values: string[] }

export interface Field {
  attr: string; label: string; type: "text" | "email" | "tel" | "textarea" | "path" | "sshkey"; multi: boolean;
  required?: boolean; userVisible: boolean; userEditable: boolean; feature?: string; readOnly?: boolean;
}
export interface FieldValue extends Field { values: string[] }
export type Attrs = Record<string, string[]>;

export interface Features {
  groups: boolean; suspendByMove: boolean; passwordMinLength: number;
  unix: boolean; sshKeys: boolean; homePattern: string; defaultShell: string;
}
export interface Me { dn: string; name: string; isAdmin: boolean; features: Features }

export interface User {
  dn: string; username: string; name: string; email: string; phones: string[] | null;
  title?: string; department?: string; suspended: boolean;
}
export interface UserPage { users: User[]; total: number; page: number; pageSize: number; truncated: boolean }
export interface UserQuery { q: string; sort: string; dir: "asc" | "desc"; status: string; page: number; pageSize: number }

export type GroupKind = "shared" | "linux" | "personal";
export interface PersonDetail {
  dn: string; username: string; name: string; suspended: boolean; isSelf: boolean;
  fields: FieldValue[]; groups: { dn: string; name: string; kind: GroupKind; primary?: boolean }[];
}
export interface Profile { dn: string; username: string; name: string; fields: FieldValue[]; groups: string[] }
export interface GroupSummary { dn: string; name: string; kind: GroupKind; gid?: string; members: number; admin?: boolean }
export interface GroupPage { groups: GroupSummary[]; total: number; page: number; pageSize: number; counts: Partial<Record<GroupKind, number>> }
export interface GroupQuery { kind?: string; q?: string; page?: number; pageSize?: number }
export interface GroupMember { dn: string; username: string; name: string }
export interface GroupDetail { dn: string; name: string; kind: GroupKind; gid?: string; members: GroupMember[]; primaryMembers: GroupMember[]; protected?: string }
export interface SettingsIO {
  ldap: { url: string; startTLS: boolean; skipVerify: boolean; bindDN: string; bindPassword?: string; bindPasswordSet?: boolean };
  users: { baseDN: string; loginAttributes: string[]; suspendedBaseDN: string; objectClasses: string[]; rdnAttribute: string };
  groups: { baseDN: string; objectClass: string; memberAttribute: string };
  admin: { groupDN: string };
  password: { minLength: number };
  session: { idleMinutes: number; maxHours: number };
  unix: { enabled: boolean; uidMin: number; uidMax: number; homePattern: string; defaultShell: string; shadow: boolean; sshKeys: boolean };
}
export interface Check { key: string; ok: boolean; warn?: boolean; message: string; missing?: string }
export interface SettingsPayload { settings: SettingsIO; locked: Record<string, boolean>; checks: Check[]; fields: Field[] }
export interface SetupAdmin { mode: "existing" | "person" | "new"; username: string; givenName: string; sn: string; password: string }
export interface OverlayParam {
  key: string; label: string; type: "bool" | "text" | "number" | "multi"; hint?: string; default: string;
  options?: { value: string; label: string }[]; advanced?: boolean;
}
export interface OverlayView {
  id: string; name: string; summary: string; why: string; warning?: string; params: OverlayParam[];
  enabled: boolean | null; values: Record<string, string>; notes?: string[];
}
export interface OverlayState { unlocked: boolean; unlockError?: string; database?: string; suffix: string; overlays: OverlayView[] }
export interface OverlayRequest { overlay: string; action: "enable" | "update" | "disable"; values: Record<string, string>; dbDN?: string; confirmed?: boolean; refresh?: boolean }
export interface OverlayPreview {
  summary: string[]; warnings: string[]; unlocked: boolean; ldif: string; dataLDIF: string; commands: { config?: string; data?: string };
}
export interface FoundGroup { dn: string; name: string; members: number; class: string }
export interface FoundPerson { dn: string; username: string; name: string }
export interface GIDPerson { dn: string; username: string; name: string }
export interface GIDPreview {
  group: string; old: string; new: string; moving: GIDPerson[]; adopted: GIDPerson[]; conflict?: string; warnings: string[]; chgrpHint?: string;
}
export interface PersonGIDPreview { person: string; old: string; new: string; group: string; warnings: string[] }
export interface GIDFix { label: string; kind: "groupGID" | "personGID" | "createGroup"; groupDN?: string; personDN?: string; gid: string; groupName?: string }
export interface GIDIssue { kind: string; title: string; detail: string; fixes: GIDFix[] }
export interface Discovery {
  ok: boolean; checks: Check[]; contexts?: string[];
  suggestions?: { usersBaseDN: string; groupsBaseDN: string; suspendedBaseDN: string; adminGroupDN: string };
  exists?: Record<string, boolean>;
  schema?: Record<string, boolean> | null;
  containers?: string[];
  unix?: { detected: boolean; accounts: number; highestUID: number; homePattern?: string; defaultShell?: string; shells?: string[] };
}

let setupCode = "";
export const setSetupCode = (c: string) => { setupCode = c; };

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = { "Content-Type": "application/json", "X-Requested-With": "dapper" };
  if (setupCode && path.startsWith("/api/setup/")) headers["X-Setup-Code"] = setupCode;
  const r = await fetch(path, { ...init, headers });
  if (r.status === 204) return undefined as T;
  let body: { error?: string } = {};
  try { body = await r.json(); } catch { /* non-JSON error page */ }
  if (!r.ok) throw new ApiError(body.error ?? "Something went wrong. Please try again.", r.status, body);
  return body as T;
}

export class ApiError extends Error {
  constructor(message: string, public status: number, public data?: unknown) { super(message); }
}

const q = (dn: string) => `dn=${encodeURIComponent(dn)}`;
const json = (method: string, body: unknown) => ({ method, body: JSON.stringify(body) });

const groupsPage = (p: GroupQuery = {}) =>
  req<GroupPage>(`/api/admin/groups?kind=${p.kind ?? ""}&q=${encodeURIComponent(p.q ?? "")}&page=${p.page ?? 1}&pageSize=${p.pageSize ?? 25}`);

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
  groups: groupsPage,
  /** Groups people can be put in: shared and Linux groups, not the one-per-person groups. */
  assignableGroups: async (): Promise<GroupSummary[]> => {
    const [a, b] = await Promise.all([groupsPage({ kind: "shared", pageSize: 500 }), groupsPage({ kind: "linux", pageSize: 500 })]);
    return [...a.groups, ...b.groups];
  },
  group: (dn: string) => req<GroupDetail>(`/api/admin/group?${q(dn)}`),
  createGroup: (name: string, kind: "standard" | "linux" = "standard") => req<{ dn: string }>("/api/admin/groups", json("POST", { name, kind })),
  deleteGroup: (dn: string) => req(`/api/admin/group?${q(dn)}`, { method: "DELETE" }),
  checkGroups: () => req<{ issues: GIDIssue[] }>("/api/admin/groups/check"),
  groupGID: (dn: string, gid: string, movePeople: boolean, confirmed = false) =>
    req<GIDPreview & { ok?: boolean; moved?: number; preview?: GIDPreview }>("/api/admin/group/gid", json("POST", { dn, gid, movePeople, confirmed })),
  personGID: (dn: string, gid: string, confirmed = false) => req<PersonGIDPreview & { ok?: boolean }>("/api/admin/user/gid", json("POST", { dn, gid, confirmed })),
  createLinuxGroup: (name: string, gid: string, adopt = false) => req<{ dn: string }>("/api/admin/groups", json("POST", { name, kind: "linux", gid, adopt })),
  addMember: (group: string, member: string) => req(`/api/admin/group/members?${q(group)}`, json("POST", { member })),
  removeMember: (group: string, member: string) =>
    req(`/api/admin/group/members?${q(group)}&member=${encodeURIComponent(member)}`, { method: "DELETE" }),

  // settings
  settings: () => req<SettingsPayload>("/api/admin/settings"),
  checkSettings: (s: SettingsIO) => req<{ ok: boolean; checks: Check[] }>("/api/admin/settings/check", json("POST", s)),
  saveSettings: (s: SettingsIO) => req<SettingsPayload>("/api/admin/settings", json("PUT", s)),
  saveFieldPolicy: (fields: Field[]) => req<{ fields: Field[] }>("/api/admin/settings/fields", json("PUT", { fields })),

  // directory server features (overlays)
  overlays: () => req<OverlayState>("/api/admin/overlays"),
  unlockOverlays: (dn: string, password: string) => req<{ unlocked: boolean }>("/api/admin/overlays/unlock", json("POST", { dn, password })),
  lockOverlays: () => req<{ unlocked: boolean }>("/api/admin/overlays/lock", { method: "POST" }),
  previewOverlay: (r: OverlayRequest) => req<OverlayPreview>("/api/admin/overlays/preview", json("POST", r)),
  applyOverlay: (r: OverlayRequest) => req<{ ok: boolean; backup: string; refreshedGroups: number }>("/api/admin/overlays/apply", json("POST", r)),
  refreshMemberOf: () => req<{ refreshedGroups: number }>("/api/admin/overlays/memberof/refresh", json("POST", { confirmed: true })),

  // first-run setup
  setupStatus: () => req<{ configured: boolean }>("/api/setup/status"),
  setupDiscover: (s: SettingsIO) => req<Discovery>("/api/setup/discover", json("POST", s)),
  setupCheck: (s: SettingsIO) => req<{ ok: boolean; checks: Check[] }>("/api/setup/check", json("POST", s)),
  setupGroups: (s: SettingsIO) => req<{ groups: FoundGroup[]; suggest: { objectClass?: string; memberAttribute?: string } }>("/api/setup/groups", json("POST", s)),
  setupPeople: (s: SettingsIO, q: string) => req<{ people: FoundPerson[] }>("/api/setup/people", json("POST", { settings: s, q })),
  createContainer: (dn: string) => req("/api/admin/settings/containers", json("POST", { dn })),
  setupComplete: (b: { settings: SettingsIO; createContainers: boolean; confirmed: boolean; admin: SetupAdmin }) =>
    req<{ ok: boolean }>("/api/setup/complete", json("POST", b)),

  // advanced (raw directory)
  children: (dn?: string) => req<Entry[]>(`/api/admin/children?${dn ? q(dn) : ""}`),
  get: (dn: string) => req<Entry>(`/api/admin/entry?${q(dn)}`),
  search: (base: string, filter: string) =>
    req<Entry[]>(`/api/admin/search?base=${encodeURIComponent(base)}&filter=${encodeURIComponent(filter)}`),
  modify: (dn: string, c: Change[]) => req(`/api/admin/entry?${q(dn)}`, json("PATCH", c)),
  remove: (dn: string) => req(`/api/admin/entry?${q(dn)}`, { method: "DELETE" }),
};
