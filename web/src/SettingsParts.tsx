import { cloneElement, isValidElement, ReactElement, ReactNode, useId } from "react";
import { Check, SettingsIO } from "./api";

export const blankSettings = (): SettingsIO => ({
  ldap: { url: "ldap://", startTLS: false, skipVerify: false, bindDN: "", bindPassword: "" },
  users: { baseDN: "", loginAttributes: ["uid", "mail"], suspendedBaseDN: "", objectClasses: ["inetOrgPerson"], rdnAttribute: "uid" },
  groups: { baseDN: "", objectClass: "groupOfNames", memberAttribute: "member" },
  admin: { groupDN: "" },
  password: { minLength: 8 },
  session: { idleMinutes: 30, maxHours: 8 },
});

export type Patch = <K extends keyof SettingsIO>(section: K, values: Partial<SettingsIO[K]>) => void;
export type Locked = Record<string, boolean>;

function Row({ label, hint, locked, children }: { label: string; hint?: ReactNode; locked?: boolean; children: ReactNode }) {
  const id = useId();
  const hintId = `${id}-hint`;
  const hasNote = Boolean(hint) || Boolean(locked);
  // A single input gets a real <label for>; a group of controls is named with aria-labelledby.
  const single = isValidElement(children) && (children as ReactElement).type === "input";
  const control = single
    ? cloneElement(children as ReactElement<Record<string, unknown>>, { id, "aria-describedby": hasNote ? hintId : undefined })
    : <div role="group" aria-labelledby={`${id}-label`} aria-describedby={hasNote ? hintId : undefined}>{children}</div>;
  return (
    <div className="setrow">
      {single
        ? <label className="setlabel" htmlFor={id}>{label}</label>
        : <span className="setlabel" id={`${id}-label`}>{label}</span>}
      <div className="setcontrol">
        {control}
        {hasNote && (
          <span className="sub" id={hintId}>
            {locked && "Set by the server's environment; it can't be changed here. "}{hint}
          </span>
        )}
      </div>
    </div>
  );
}

export function ConnectionFields({ s, patch, locked }: { s: SettingsIO; patch: Patch; locked: Locked }) {
  return (
    <>
      <Row label="Server address" locked={locked["ldap.url"]} hint="For example ldap://ldap.example.org:389 or ldaps://ldap.example.org:636">
        <input value={s.ldap.url} disabled={locked["ldap.url"]} onChange={(e) => patch("ldap", { url: e.target.value })} placeholder="ldaps://ldap.example.org" />
      </Row>
      <Row label="Encryption" locked={locked["ldap.startTLS"]}>
        <span className="checks">
          <label className="inline"><input type="checkbox" checked={s.ldap.startTLS} disabled={locked["ldap.startTLS"]}
            onChange={(e) => patch("ldap", { startTLS: e.target.checked })} /> Upgrade the connection with StartTLS</label>
          <label className="inline"><input type="checkbox" checked={s.ldap.skipVerify} disabled={locked["ldap.skipVerify"]}
            onChange={(e) => patch("ldap", { skipVerify: e.target.checked })} /> Don't verify the server's certificate (testing only)</label>
        </span>
      </Row>
      <Row label="Service account" locked={locked["ldap.bindDN"]} hint="The account Dapper uses to look people up and make changes. See the documentation for the access it needs.">
        <input value={s.ldap.bindDN} disabled={locked["ldap.bindDN"]} onChange={(e) => patch("ldap", { bindDN: e.target.value })} placeholder="cn=dapper,dc=example,dc=org" />
      </Row>
      <Row label="Service account password" locked={locked["ldap.bindPassword"]}
        hint={s.ldap.bindPasswordSet ? "A password is saved. Leave this blank to keep it." : undefined}>
        <input type="password" autoComplete="new-password" value={s.ldap.bindPassword ?? ""} disabled={locked["ldap.bindPassword"]}
          onChange={(e) => patch("ldap", { bindPassword: e.target.value })} placeholder={s.ldap.bindPasswordSet ? "••••••••" : ""} />
      </Row>
    </>
  );
}

export function LayoutFields({ s, patch, locked }: { s: SettingsIO; patch: Patch; locked: Locked }) {
  return (
    <>
      <Row label="Where people are" locked={locked["users.baseDN"]} hint="The container that holds the user accounts.">
        <input value={s.users.baseDN} disabled={locked["users.baseDN"]} onChange={(e) => patch("users", { baseDN: e.target.value })} placeholder="ou=people,dc=example,dc=org" />
      </Row>
      <Row label="Where groups are" locked={locked["groups.baseDN"]} hint="Optional. Leave empty to turn off group management.">
        <input value={s.groups.baseDN} disabled={locked["groups.baseDN"]} onChange={(e) => patch("groups", { baseDN: e.target.value })} placeholder="ou=groups,dc=example,dc=org" />
      </Row>
      <Row label="Where suspended people go" locked={locked["users.suspendedBaseDN"]}
        hint="Optional. Suspending someone moves them here, which works on any server. Leave empty to lock accounts in place instead (needs the ppolicy overlay).">
        <input value={s.users.suspendedBaseDN} disabled={locked["users.suspendedBaseDN"]} onChange={(e) => patch("users", { suspendedBaseDN: e.target.value })} placeholder="ou=suspended,dc=example,dc=org" />
      </Row>
      <Row label="People sign in with" locked={locked["users.loginAttributes"]} hint="Separate with commas. Usually uid and mail, so people can type a username or an email.">
        <input value={s.users.loginAttributes.join(", ")} disabled={locked["users.loginAttributes"]}
          onChange={(e) => patch("users", { loginAttributes: e.target.value.split(",").map((x) => x.trim()) })} />
      </Row>
    </>
  );
}

export function AdminGroupField({ s, patch, locked }: { s: SettingsIO; patch: Patch; locked: Locked }) {
  return (
    <Row label="Administrators group" locked={locked["admin.groupDN"]} hint="Members of this group can manage everyone in Dapper.">
      <input value={s.admin.groupDN} disabled={locked["admin.groupDN"]} onChange={(e) => patch("admin", { groupDN: e.target.value })} placeholder="cn=dapper-admins,ou=groups,dc=example,dc=org" />
    </Row>
  );
}

export function PasswordSessionFields({ s, patch }: { s: SettingsIO; patch: Patch }) {
  const num = (v: string) => (v === "" ? 0 : Number(v));
  return (
    <>
      <Row label="Minimum password length" hint="Dapper enforces this in addition to your directory's own password rules.">
        <input type="number" min={1} max={128} value={s.password.minLength || ""} onChange={(e) => patch("password", { minLength: num(e.target.value) })} />
      </Row>
      <Row label="Sign people out after" hint="Minutes without activity.">
        <input type="number" min={1} value={s.session.idleMinutes || ""} onChange={(e) => patch("session", { idleMinutes: num(e.target.value) })} />
      </Row>
      <Row label="Always sign people out after" hint="Hours, even if they stay active.">
        <input type="number" min={1} value={s.session.maxHours || ""} onChange={(e) => patch("session", { maxHours: num(e.target.value) })} />
      </Row>
    </>
  );
}

export function AdvancedFields({ s, patch }: { s: SettingsIO; patch: Patch }) {
  return (
    <details className="advanced">
      <summary>Advanced directory options</summary>
      <p className="sub">Only change these if your directory uses a different layout than the usual OpenLDAP defaults.</p>
      <Row label="Account object classes" hint="Applied to new accounts. Separate with commas.">
        <input value={s.users.objectClasses.join(", ")} onChange={(e) => patch("users", { objectClasses: e.target.value.split(",").map((x) => x.trim()) })} />
      </Row>
      <Row label="Account naming attribute" hint="The first part of a new account's name, usually uid.">
        <input value={s.users.rdnAttribute} onChange={(e) => patch("users", { rdnAttribute: e.target.value })} />
      </Row>
      <Row label="Group object class">
        <input value={s.groups.objectClass} onChange={(e) => patch("groups", { objectClass: e.target.value })} />
      </Row>
      <Row label="Group member attribute">
        <input value={s.groups.memberAttribute} onChange={(e) => patch("groups", { memberAttribute: e.target.value })} />
      </Row>
    </details>
  );
}

/** A readable checklist of what was tested. `pending` marks things the wizard will create. */
export function Checks({ checks, pending }: { checks: Check[]; pending?: (c: Check) => boolean }) {
  return (
    <ul className="checklist" aria-label="Connection checks">
      {checks.map((c) => {
        const will = !c.ok && pending?.(c);
        const cls = will ? "pend" : c.ok ? (c.warn ? "warn" : "pass") : "bad";
        const icon = will ? "＋" : c.ok ? (c.warn ? "!" : "✓") : "✗";
        return (
          <li key={c.key} className={cls}>
            <span className="ico" aria-hidden="true">{icon}</span>
            <span>{will ? c.message.replace("doesn't exist yet.", "doesn't exist yet and will be created.") : c.message}</span>
          </li>
        );
      })}
    </ul>
  );
}
