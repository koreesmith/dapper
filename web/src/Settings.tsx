import { useEffect, useState } from "react";
import { api, ApiError, Check, Field, SettingsIO } from "./api";
import { AdminGroupField, AdvancedFields, Checks, ConnectionFields, LayoutFields, Locked, PasswordSessionFields, Patch, UnixFields } from "./SettingsParts";
import { Modal, Notice } from "./ui";

type Msg = { kind: "ok" | "error"; text: string } | null;

export function Settings() {
  const [saved, setSaved] = useState<SettingsIO | null>(null);
  const [s, setS] = useState<SettingsIO | null>(null);
  const [locked, setLocked] = useState<Locked>({});
  const [checks, setChecks] = useState<Check[]>([]);
  const [fields, setFields] = useState<Field[]>([]);
  const [savedFields, setSavedFields] = useState<Field[]>([]);
  const [msg, setMsg] = useState<Msg>(null);
  const [fmsg, setFmsg] = useState<Msg>(null);
  const [busy, setBusy] = useState(false);
  const [toCreate, setToCreate] = useState<string | null>(null);

  const load = () => api.settings().then((d) => {
    setSaved(d.settings); setS(d.settings); setLocked(d.locked); setChecks(d.checks);
    setFields(d.fields); setSavedFields(d.fields);
  }).catch((x) => setMsg({ kind: "error", text: (x as Error).message }));
  useEffect(() => { load(); }, []);

  if (!s || !saved) return <div className="page">{msg ? <Notice kind="error">{msg.text}</Notice> : "Loading…"}</div>;

  const patch: Patch = (section, values) => setS((cur) => cur && ({ ...cur, [section]: { ...cur[section], ...values } }));
  const dirty = JSON.stringify(s) !== JSON.stringify(saved);

  const test = async () => {
    setBusy(true); setMsg(null);
    try { const r = await api.checkSettings(s); setChecks(r.checks); setMsg(r.ok ? { kind: "ok", text: "These settings work." } : { kind: "error", text: "There's a problem with these settings. See the checks below." }); }
    catch (x) { setMsg({ kind: "error", text: (x as Error).message }); } finally { setBusy(false); }
  };
  const save = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true); setMsg(null);
    try {
      const r = await api.saveSettings(s);
      setSaved(r.settings); setS(r.settings); setLocked(r.locked); setChecks(r.checks);
      setMsg({ kind: "ok", text: "Settings saved. They take effect immediately." });
    } catch (x) {
      setMsg({ kind: "error", text: (x as Error).message });
      const d = (x as ApiError).data as { checks?: Check[] } | undefined;
      if (d?.checks) setChecks(d.checks);
    } finally { setBusy(false); }
  };

  const fset = (attr: string, patchF: Partial<Field>) => setFields(fields.map((f) => (f.attr === attr ? { ...f, ...patchF } : f)));
  const fdirty = JSON.stringify(fields) !== JSON.stringify(savedFields);

  return (
    <div className="page narrow">
      <h2>Settings</h2>

      <form className="panel" id="conn-form" onSubmit={save}>
        <h3>Directory connection</h3>
        <ConnectionFields s={s} patch={patch} locked={locked} />
        <h3>Where things live</h3>
        <LayoutFields s={s} patch={patch} locked={locked} />
        <AdminGroupField s={s} patch={patch} locked={locked} />
        <h3>Linux accounts</h3>
        <UnixFields s={s} patch={patch} />
        <h3>Passwords and sign-out</h3>
        <PasswordSessionFields s={s} patch={patch} />
        <AdvancedFields s={s} patch={patch} />

        <h3>Status</h3>
        <Checks checks={checks} onCreate={(c) => c.missing && setToCreate(c.missing)} />
        {msg && <Notice kind={msg.kind}>{msg.text}</Notice>}
        <div className="formactions">
          <button className="primary" disabled={!dirty || busy}>{busy ? "Working…" : "Save settings"}</button>
          <button type="button" disabled={busy} onClick={test}>Test these settings</button>
          {dirty && <button type="button" onClick={() => { setS(saved); setMsg(null); }}>Discard changes</button>}
        </div>
        <p className="sub">Settings are tested before they're saved, and a change that would lock you out of the admin screens is refused.</p>
      </form>

      {dirty && (
        <div className="savebar" role="region" aria-label="Unsaved changes">
          <span>You have unsaved changes to the settings above.</span>
          <button className="primary" form="conn-form" disabled={busy}>{busy ? "Working…" : "Save settings"}</button>
          <button type="button" onClick={() => { setS(saved); setMsg(null); }}>Discard</button>
        </div>
      )}

      {toCreate && (
        <Modal title="Create this container?" onClose={() => setToCreate(null)}>
          <p>Dapper will add an empty container named <strong>{toCreate}</strong> to your directory. Nothing else changes.</p>
          <div className="formactions">
            <button className="primary" onClick={async () => {
              try { await api.createContainer(toCreate); setToCreate(null); const r = await api.checkSettings(s); setChecks(r.checks); setMsg({ kind: "ok", text: "The container was created." }); }
              catch (x) { setToCreate(null); setMsg({ kind: "error", text: (x as Error).message }); }
            }}>Create it</button>
            <button onClick={() => setToCreate(null)}>Cancel</button>
          </div>
        </Modal>
      )}

      <form className="panel" onSubmit={async (e) => {
        e.preventDefault(); setBusy(true); setFmsg(null);
        try { const r = await api.saveFieldPolicy(fields); setFields(r.fields); setSavedFields(r.fields); setFmsg({ kind: "ok", text: "Saved. The new rules apply immediately." }); }
        catch (x) { setFmsg({ kind: "error", text: (x as Error).message }); } finally { setBusy(false); }
      }}>
        <h3>What people can see and change about themselves</h3>
        <p className="sub">This table has its own save button (below it), separate from the settings above.</p>
        <p className="sub">Administrators can always see and edit everything. These switches control the “My account” page for everyone else.</p>
        <table className="users">
          <thead><tr><th>Detail</th><th>Can see</th><th>Can change</th></tr></thead>
          <tbody>
            {fields.map((f) => (
              <tr key={f.attr}>
                <td>{f.label}</td>
                <td><input type="checkbox" aria-label={`People can see their ${f.label}`} checked={f.userVisible || f.userEditable}
                  disabled={f.userEditable} onChange={(e) => fset(f.attr, { userVisible: e.target.checked })} /></td>
                <td><input type="checkbox" aria-label={`People can change their ${f.label}`} checked={f.userEditable}
                  onChange={(e) => fset(f.attr, { userEditable: e.target.checked, userVisible: e.target.checked || f.userVisible })} /></td>
              </tr>
            ))}
          </tbody>
        </table>
        <div className="formactions">
          <button className="primary" disabled={!fdirty || busy}>Save these choices</button>
          {fdirty && <button type="button" onClick={() => setFields(savedFields)}>Discard</button>}
        </div>
        {fmsg && <Notice kind={fmsg.kind}>{fmsg.text}</Notice>}
      </form>
    </div>
  );
}
