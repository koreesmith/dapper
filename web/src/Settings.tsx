import { useEffect, useState } from "react";
import { api, Field, Settings as S } from "./api";
import { Notice } from "./ui";

export function Settings() {
  const [s, setS] = useState<S | null>(null);
  const [fields, setFields] = useState<Field[]>([]);
  const [msg, setMsg] = useState<{ kind: "ok" | "error"; text: string } | null>(null);
  const [saving, setSaving] = useState(false);

  const load = () => api.settings().then((d) => { setS(d); setFields(d.fields); })
    .catch((x) => setMsg({ kind: "error", text: (x as Error).message }));
  useEffect(() => { load(); }, []);

  if (!s) return <div className="page">{msg ? <Notice kind="error">{msg.text}</Notice> : "Loading…"}</div>;

  const set = (attr: string, patch: Partial<Field>) => setFields(fields.map((f) => (f.attr === attr ? { ...f, ...patch } : f)));
  const dirty = JSON.stringify(fields) !== JSON.stringify(s.fields);
  const c = s.connection;

  return (
    <div className="page narrow">
      <h2>Settings</h2>

      <div className="panel">
        <h3>Directory connection</h3>
        <p>{c.ok ? <span className="badge on">Connected</span> : <span className="badge off">Problem</span>} {!c.ok && <span className="sub">{c.message}</span>}</p>
        <dl className="facts">
          <dt>Server</dt><dd>{c.url}{c.startTLS ? " (StartTLS)" : ""}</dd>
          <dt>Service account</dt><dd>{c.bindDN}</dd>
          <dt>People are in</dt><dd>{c.usersBaseDN}</dd>
          <dt>Groups are in</dt><dd>{c.groupsBaseDN || "Not configured"}</dd>
          <dt>Suspended people go to</dt><dd>{c.suspendedBaseDN || "Locked in place (ppolicy)"}</dd>
          <dt>Administrators group</dt><dd>{c.adminGroupDN}</dd>
        </dl>
        <p className="sub">These are set in Dapper's configuration file or environment, not here. The service account's password is never shown.</p>
        <button onClick={() => { setS(null); load(); }}>Check again</button>
      </div>

      <form className="panel" onSubmit={async (e) => {
        e.preventDefault(); setSaving(true); setMsg(null);
        try { const r = await api.saveFieldPolicy(fields); setFields(r.fields); setS({ ...s, fields: r.fields }); setMsg({ kind: "ok", text: "Saved. The new rules apply immediately." }); }
        catch (x) { setMsg({ kind: "error", text: (x as Error).message }); } finally { setSaving(false); }
      }}>
        <h3>What people can see and change about themselves</h3>
        <p className="sub">Administrators can always see and edit everything. These switches control the “My account” page for everyone else.</p>
        <table className="users">
          <thead><tr><th>Detail</th><th>Can see</th><th>Can change</th></tr></thead>
          <tbody>
            {fields.map((f) => (
              <tr key={f.attr}>
                <td>{f.label}</td>
                <td><input type="checkbox" aria-label={`People can see their ${f.label}`} checked={f.userVisible || f.userEditable}
                  disabled={f.userEditable} onChange={(e) => set(f.attr, { userVisible: e.target.checked })} /></td>
                <td><input type="checkbox" aria-label={`People can change their ${f.label}`} checked={f.userEditable}
                  onChange={(e) => set(f.attr, { userEditable: e.target.checked, userVisible: e.target.checked || f.userVisible })} /></td>
              </tr>
            ))}
          </tbody>
        </table>
        <div className="formactions">
          <button className="primary" disabled={!dirty || saving}>{saving ? "Saving…" : "Save"}</button>
          {dirty && <button type="button" onClick={() => setFields(s.fields)}>Discard</button>}
        </div>
        {msg && <Notice kind={msg.kind}>{msg.text}</Notice>}
      </form>
    </div>
  );
}
