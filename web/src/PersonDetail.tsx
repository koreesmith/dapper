import { useCallback, useEffect, useState } from "react";
import { api, GroupSummary, Me, PersonDetail as Person } from "./api";
import { changedAttrs, FieldEditor, FormState, initialValues, Modal, Notice, SecretBox } from "./ui";
import { go, href } from "./router";

export function PersonDetail({ dn, me }: { dn: string; me: Me }) {
  const [p, setP] = useState<Person | null>(null);
  const [form, setForm] = useState<FormState>({});
  const [msg, setMsg] = useState<{ kind: "ok" | "error"; text: string } | null>(null);
  const [saving, setSaving] = useState(false);
  const [dialog, setDialog] = useState<"reset" | "delete" | null>(null);
  const [allGroups, setAllGroups] = useState<GroupSummary[]>([]);

  const load = useCallback(() => api.person(dn).then((d) => {
    setP(d); setForm(initialValues(d.fields));
  }).catch((x) => setMsg({ kind: "error", text: (x as Error).message })), [dn]);

  useEffect(() => { setP(null); setMsg(null); load(); }, [load]);
  useEffect(() => { if (me.features.groups) api.groups().then(setAllGroups).catch(() => {}); }, [me.features.groups]);

  if (!p) return <div className="page">{msg ? <Notice kind="error">{msg.text}</Notice> : "Loading…"}<p><a href="#/people">← Back to people</a></p></div>;

  const changes = changedAttrs(p.fields.filter((f) => !f.readOnly), form);
  const dirty = Object.keys(changes).length > 0;

  const save = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true); setMsg(null);
    try { await api.updatePerson(dn, changes); setMsg({ kind: "ok", text: "Changes saved." }); await load(); }
    catch (x) { setMsg({ kind: "error", text: (x as Error).message }); }
    finally { setSaving(false); }
  };

  const toggleSuspend = async () => {
    setMsg(null);
    try {
      const r = p.suspended ? await api.restore(dn) : await api.suspend(dn);
      if (r.dn !== dn) go(href("/person", r.dn)); else await load();
    } catch (x) { setMsg({ kind: "error", text: (x as Error).message }); }
  };

  const memberOf = new Set(p.groups.map((g) => g.dn.toLowerCase()));
  const addable = allGroups.filter((g) => !memberOf.has(g.dn.toLowerCase()));

  return (
    <div className="page narrow">
      <p><a href="#/people">← Back to people</a></p>
      <div className="pagehead">
        <div>
          <h2>{p.name} {p.suspended ? <span className="badge off">Suspended</span> : <span className="badge on">Active</span>}</h2>
          <div className="sub">Username: {p.username}</div>
        </div>
        <div className="actions">
          {!p.isSelf && <button onClick={toggleSuspend}>{p.suspended ? "Restore account" : "Suspend account"}</button>}
          <button onClick={() => setDialog("reset")}>Reset password</button>
          {!p.isSelf && <button className="danger" onClick={() => setDialog("delete")}>Delete…</button>}
        </div>
      </div>
      {p.suspended && <p className="sub">This account is suspended and can't sign in. Restore it to let them back in.</p>}

      <form className="panel" onSubmit={save}>
        <h3>Details</h3>
        {p.fields.map((f) => (
          <FieldEditor key={f.attr} field={f} readOnly={f.readOnly} values={form[f.attr] ?? []} onChange={(v) => setForm({ ...form, [f.attr]: v })} />
        ))}
        <div className="formactions">
          <button className="primary" disabled={!dirty || saving}>{saving ? "Saving…" : "Save changes"}</button>
          {dirty && <button type="button" onClick={() => setForm(initialValues(p.fields))}>Discard</button>}
        </div>
        {msg && <Notice kind={msg.kind}>{msg.text}</Notice>}
      </form>

      {me.features.groups && (
        <div className="panel">
          <h3>Groups</h3>
          {p.groups.length === 0 && <p className="sub">Not in any groups.</p>}
          <ul className="chips">
            {p.groups.map((g) => (
              <li key={g.dn}>
                <a href={href("#/group", g.dn)}>{g.name}</a>
                <button className="plain" aria-label={`Remove from ${g.name}`} onClick={async () => {
                  try { await api.removeMember(g.dn, p.dn); await load(); } catch (x) { setMsg({ kind: "error", text: (x as Error).message }); }
                }}>✕</button>
              </li>
            ))}
          </ul>
          {addable.length > 0 && (
            <label className="inline">Add to group
              <select value="" onChange={async (e) => {
                if (!e.target.value) return;
                try { await api.addMember(e.target.value, p.dn); await load(); } catch (x) { setMsg({ kind: "error", text: (x as Error).message }); }
              }}>
                <option value="">Choose a group…</option>
                {addable.map((g) => <option key={g.dn} value={g.dn}>{g.name}</option>)}
              </select>
            </label>
          )}
        </div>
      )}

      {dialog === "reset" && <ResetDialog p={p} minLen={me.features.passwordMinLength} onClose={() => setDialog(null)} />}
      {dialog === "delete" && <DeleteDialog p={p} onClose={() => setDialog(null)} />}
    </div>
  );
}

function ResetDialog({ p, minLen, onClose }: { p: Person; minLen: number; onClose: () => void }) {
  const [mode, setMode] = useState<"generate" | "set">("generate");
  const [pw, setPw] = useState("");
  const [result, setResult] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr("");
    try {
      const r = await api.resetPassword(p.dn, mode === "set" ? pw : undefined);
      if (r.temporaryPassword) setResult(r.temporaryPassword); else setDone(true);
    } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
  };

  return (
    <Modal title={`Reset password for ${p.name}`} onClose={onClose}>
      {result || done ? (
        <>
          {result && <><p>Give {p.name} this temporary password. It won't be shown again.</p><SecretBox value={result} /></>}
          {done && <Notice kind="ok">The password was changed.</Notice>}
          <div className="formactions"><button className="primary" onClick={onClose}>Done</button></div>
        </>
      ) : (
        <form onSubmit={submit}>
          <label className="inline"><input type="radio" checked={mode === "generate"} onChange={() => setMode("generate")} /> Generate a temporary password</label>
          <label className="inline"><input type="radio" checked={mode === "set"} onChange={() => setMode("set")} /> Choose a password myself</label>
          {mode === "set" && (
            <label>New password (at least {minLen} characters)
              <input type="text" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="off" autoFocus />
            </label>
          )}
          {err && <Notice kind="error">{err}</Notice>}
          <div className="formactions">
            <button className="primary" disabled={busy || (mode === "set" && pw.length < minLen)}>Reset password</button>
            <button type="button" onClick={onClose}>Cancel</button>
          </div>
        </form>
      )}
    </Modal>
  );
}

function DeleteDialog({ p, onClose }: { p: Person; onClose: () => void }) {
  const [typed, setTyped] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Modal title={`Delete ${p.name}?`} onClose={onClose}>
      <p>This permanently removes the account and its group memberships. It can't be undone. If you only want to block sign-in, <strong>suspend</strong> the account instead.</p>
      <label>Type <strong>{p.username}</strong> to confirm
        <input value={typed} onChange={(e) => setTyped(e.target.value)} autoFocus autoComplete="off" />
      </label>
      {err && <Notice kind="error">{err}</Notice>}
      <div className="formactions">
        <button className="danger" disabled={typed !== p.username || busy} onClick={async () => {
          setBusy(true);
          try { await api.deletePerson(p.dn); go("/people"); } catch (x) { setErr((x as Error).message); setBusy(false); }
        }}>Delete permanently</button>
        <button onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}
