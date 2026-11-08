import { useEffect, useState } from "react";
import { api, Me, Profile } from "./api";
import { changedAttrs, FieldEditor, FormState, initialValues, Notice, StrengthMeter } from "./ui";

export function MyAccount({ me }: { me: Me }) {
  const [p, setP] = useState<Profile | null>(null);
  const [form, setForm] = useState<FormState>({});
  const [msg, setMsg] = useState<{ kind: "ok" | "error"; text: string } | null>(null);
  const [saving, setSaving] = useState(false);

  const load = () => api.profile().then((d) => { setP(d); setForm(initialValues(d.fields)); })
    .catch((x) => setMsg({ kind: "error", text: (x as Error).message }));
  useEffect(() => { load(); }, []);

  if (!p) return <div className="page">{msg ? <Notice kind="error">{msg.text}</Notice> : "Loading…"}</div>;

  const editable = p.fields.filter((f) => f.userEditable && !f.readOnly);
  const readonly = p.fields.filter((f) => !f.userEditable || f.readOnly);
  const changes = changedAttrs(editable, form);
  const dirty = Object.keys(changes).length > 0;

  return (
    <div className="page narrow">
      <h2>My account</h2>
      <div className="sub">Signed in as {p.username || me.name}</div>

      <form className="panel" onSubmit={async (e) => {
        e.preventDefault(); setSaving(true); setMsg(null);
        try { await api.updateProfile(changes); setMsg({ kind: "ok", text: "Your details were saved." }); await load(); }
        catch (x) { setMsg({ kind: "error", text: (x as Error).message }); } finally { setSaving(false); }
      }}>
        <h3>My details</h3>
        {editable.map((f) => <FieldEditor key={f.attr} field={f} values={form[f.attr] ?? []} onChange={(v) => setForm({ ...form, [f.attr]: v })} />)}
        {readonly.map((f) => <FieldEditor key={f.attr} field={f} values={f.values} onChange={() => {}} readOnly />)}
        {readonly.length > 0 && <p className="sub">Details shown without a box can only be changed by an administrator.</p>}
        {editable.length > 0 && (
          <div className="formactions">
            <button className="primary" disabled={!dirty || saving}>{saving ? "Saving…" : "Save changes"}</button>
            {dirty && <button type="button" onClick={() => setForm(initialValues(p.fields))}>Discard</button>}
          </div>
        )}
        {msg && <Notice kind={msg.kind}>{msg.text}</Notice>}
      </form>

      {me.features.groups && p.groups.length > 0 && (
        <div className="panel">
          <h3>My groups</h3>
          <ul className="chips">{p.groups.map((g) => <li key={g}><span>{g}</span></li>)}</ul>
        </div>
      )}

      <PasswordCard minLen={me.features.passwordMinLength} />
    </div>
  );
}

function PasswordCard({ minLen }: { minLen: number }) {
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [again, setAgain] = useState("");
  const [msg, setMsg] = useState<{ kind: "ok" | "error"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  const mismatch = again !== "" && next !== again;
  const ok = cur !== "" && next.length >= minLen && next === again;

  return (
    <form className="panel" onSubmit={async (e) => {
      e.preventDefault(); setBusy(true); setMsg(null);
      try {
        await api.changePassword(cur, next);
        setMsg({ kind: "ok", text: "Your password was changed." });
        setCur(""); setNext(""); setAgain("");
      } catch (x) { setMsg({ kind: "error", text: (x as Error).message }); } finally { setBusy(false); }
    }}>
      <h3>Change my password</h3>
      <label>Current password<input type="password" autoComplete="current-password" value={cur} onChange={(e) => setCur(e.target.value)} /></label>
      <label>New password
        <input type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
        <span className="sub">At least {minLen} characters. A longer phrase is stronger than a short complicated one.</span>
      </label>
      <StrengthMeter password={next} />
      <label>Type the new password again
        <input type="password" autoComplete="new-password" value={again} onChange={(e) => setAgain(e.target.value)} aria-invalid={mismatch} />
      </label>
      {mismatch && <Notice kind="error">The two passwords don't match.</Notice>}
      {msg && <Notice kind={msg.kind}>{msg.text}</Notice>}
      <div className="formactions"><button className="primary" disabled={!ok || busy}>{busy ? "Changing…" : "Change password"}</button></div>
    </form>
  );
}
