import { useCallback, useEffect, useState } from "react";
import { api, ServerPreview, ServiceAccount, ServiceAccountsState, ServiceResult, ServiceTest } from "./api";
import { Modal, Notice, SecretBox } from "./ui";

/** Read-only accounts for applications and machines, so they don't need the directory manager. */
export function ServiceAccounts() {
  const [st, setSt] = useState<ServiceAccountsState | null>(null);
  const [err, setErr] = useState("");
  const [creating, setCreating] = useState(false);
  const [resetting, setResetting] = useState<ServiceAccount | null>(null);
  const [result, setResult] = useState<{ r: ServiceResult; password: string } | null>(null);

  const load = useCallback(() => api.serviceAccounts().then(setSt).catch((x) => setErr((x as Error).message)), []);
  useEffect(() => { load(); }, [load]);

  if (!st) return <div className="page">{err ? <Notice kind="error">{err}</Notice> : "Loading…"}</div>;

  return (
    <div className="page narrow">
      <div className="pagehead">
        <h2>Service accounts</h2>
        <button className="primary" onClick={() => setCreating(true)}>+ New service account</button>
      </div>
      <p className="sub">Give each application or group of machines its own account to look people up, instead of the directory manager. A service account is not a person: it has no home directory and no rights to change anything, and you can reset or delete it without touching anything else. Whether it can <em>read</em> the directory is decided by your server's access rules; Dapper checks that for you and can add the one rule that allows it.</p>
      {err && <Notice kind="error">{err}</Notice>}

      {st.accounts.length === 0 ? (
        <div className="panel"><p>No service accounts yet.{!st.containerExists && <> Creating the first one also creates the container <code>{st.container}</code>.</>}</p></div>
      ) : (
        <table className="users" tabIndex={0} aria-label="Service accounts">
          <thead><tr><th scope="col">Name</th><th scope="col">Signs in as</th><th scope="col">Used for</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead>
          <tbody>
            {st.accounts.map((a) => (
              <tr key={a.dn}>
                <td><strong>{a.name}</strong></td>
                <td><code>{a.dn}</code></td>
                <td>{a.description || <span className="sub">no description</span>}</td>
                <td className="actions">
                  <button onClick={() => setResetting(a)}>Reset password</button>{" "}
                  <button className="danger" onClick={async () => {
                    if (!window.confirm(`Delete the service account “${a.name}”? Whatever signs in with it will stop working.`)) return;
                    try { await api.deleteServiceAccount(a.dn); await load(); } catch (x) { setErr((x as Error).message); }
                  }}>Delete</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {creating && <CreateDialog st={st} onClose={() => setCreating(false)} onDone={(r, password) => { setCreating(false); setResult({ r, password }); load(); }} />}
      {resetting && <ResetDialog account={resetting} onClose={() => setResetting(null)} onDone={(r, password) => { setResetting(null); setResult({ r, password }); }} />}
      {result && <ResultDialog st={st} r={result.r} password={result.password} onClose={() => setResult(null)} />}
    </div>
  );
}

function PasswordChoice({ own, setOwn, pw, setPw }: { own: boolean; setOwn: (b: boolean) => void; pw: string; setPw: (s: string) => void }) {
  return (
    <fieldset>
      <legend>Password</legend>
      <label className="inline"><input type="radio" checked={!own} onChange={() => setOwn(false)} /> Generate a strong password (recommended). It is shown once.</label>
      <label className="inline"><input type="radio" checked={own} onChange={() => setOwn(true)} /> I'll choose one</label>
      {own && <label>Password<input type="password" autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} /><span className="sub">At least 12 characters.</span></label>}
    </fieldset>
  );
}

function CreateDialog({ st, onClose, onDone }: { st: ServiceAccountsState; onClose: () => void; onDone: (r: ServiceResult, pw: string) => void }) {
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [own, setOwn] = useState(false);
  const [pw, setPw] = useState("");
  const [ok, setOk] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const valid = /^[a-z][a-z0-9_-]{1,30}$/.test(name) && (!own || pw.length >= 12);
  const dn = `cn=${name || "name"},${st.container}`;
  return (
    <Modal title="New service account" onClose={onClose}>
      <label>Name<input value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="sssd, nextcloud, zabbix…" autoCapitalize="none" spellCheck={false} autoFocus />
        <span className="sub">Lower-case letters, numbers, - and _. It becomes the sign-in name.</span></label>
      <label>What it's used for<input value={desc} onChange={(e) => setDesc(e.target.value)} placeholder="Nextcloud login lookups" maxLength={200} /></label>
      <PasswordChoice own={own} setOwn={setOwn} pw={pw} setPw={setPw} />
      <div className="plan">
        <h3>Dapper will make these changes in your directory</h3>
        <ul>
          {!st.containerExists && <li>Create the container <code>{st.container}</code> for service accounts</li>}
          <li>Create the account <code>{dn}</code>. It gets no rights to change anything.</li>
        </ul>
        <label className="inline"><input type="checkbox" checked={ok} onChange={(e) => setOk(e.target.checked)} /> Yes, create it</label>
      </div>
      {err && <Notice kind="error">{err}</Notice>}
      <div className="formactions">
        <button onClick={onClose}>Cancel</button>
        <button className="primary" disabled={!valid || !ok || busy} onClick={async () => {
          setBusy(true); setErr("");
          try { const r = await api.createServiceAccount({ name, description: desc, password: own ? pw : undefined }); onDone(r, r.password ?? pw); }
          catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
        }}>{busy ? "Creating…" : "Create"}</button>
      </div>
    </Modal>
  );
}

function ResetDialog({ account, onClose, onDone }: { account: ServiceAccount; onClose: () => void; onDone: (r: ServiceResult, pw: string) => void }) {
  const [own, setOwn] = useState(false);
  const [pw, setPw] = useState("");
  const [ok, setOk] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  return (
    <Modal title={`Reset the password of ${account.name}`} onClose={onClose}>
      <p>Whatever signs in as <code>{account.dn}</code> will stop working until you give it the new password.</p>
      <PasswordChoice own={own} setOwn={setOwn} pw={pw} setPw={setPw} />
      <label className="inline"><input type="checkbox" checked={ok} onChange={(e) => setOk(e.target.checked)} /> Yes, set a new password</label>
      {err && <Notice kind="error">{err}</Notice>}
      <div className="formactions">
        <button onClick={onClose}>Cancel</button>
        <button className="primary" disabled={!ok || (own && pw.length < 12) || busy} onClick={async () => {
          setBusy(true); setErr("");
          try { const r = await api.resetServicePassword(account.dn, own ? pw : undefined); onDone(r, r.password ?? pw); }
          catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
        }}>{busy ? "Working…" : "Reset password"}</button>
      </div>
    </Modal>
  );
}

function TestSummary({ t }: { t: ServiceTest }) {
  if (!t.signedIn) return <Notice kind="error">{t.error ?? "The account couldn't sign in."}</Notice>;
  return (
    <>
      {t.canRead > 0 && !t.seesHashes && <Notice kind="ok">Checked: it signs in, can read people ({t.canRead >= 5 ? "5 or more" : t.canRead} found) and cannot see password hashes.</Notice>}
      {t.canRead > 0 && t.seesHashes && <Notice kind="warn">It signs in and can read people, but your access rules also let it read password hashes. Tighten them under Server → Access rules.</Notice>}
      {t.canRead === 0 && <Notice kind="warn">It signs in, but it can't see any people yet: your server's access rules don't let it read the directory.</Notice>}
      {t.error && <Notice kind="warn">{t.error}</Notice>}
    </>
  );
}

/** Shown after creating or resetting: the password once, the settings to give the application, and the read check. */
function ResultDialog({ st, r, password, onClose }: { st: ServiceAccountsState; r: ServiceResult; password: string; onClose: () => void }) {
  const [test, setTest] = useState<ServiceTest>(r.test);
  const address = st.dapperRun && st.host ? `ldap://${st.host}:389 with StartTLS, or ldaps://${st.host}:636` : st.serverURL;
  return (
    <Modal title="Service account ready" onClose={onClose}>
      {r.password
        ? <Notice kind="warn">Save this password now. It is shown only this once, and Dapper does not keep it.</Notice>
        : <p className="sub">The password is the one you chose. Dapper does not keep it.</p>}
      {r.password && <SecretBox value={r.password} />}
      <TestSummary t={test} />
      {test.signedIn && test.canRead === 0 && <AccessFix st={st} onFixed={async () => { const t = await api.testServiceAccount(r.dn, password); setTest(t.test); }} />}
      <h3>Give the application these settings</h3>
      <ul>
        <li>Bind DN (user name): <code>{r.dn}</code></li>
        <li>Server: <code>{address}</code></li>
        <li>Search base: <code>{st.searchBase}</code> (people are under <code>{st.usersBase}</code>)</li>
      </ul>
      <div className="formactions"><button className="primary" onClick={onClose}>Done</button></div>
    </Modal>
  );
}

/** Offers the one access rule that lets service accounts read, when the server settings are unlocked. */
function AccessFix({ st, onFixed }: { st: ServiceAccountsState; onFixed: () => Promise<void> }) {
  const [unlocked, setUnlocked] = useState<boolean | null>(null);
  const [plan, setPlan] = useState<ServerPreview | null>(null);
  const [ok, setOk] = useState(false);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  useEffect(() => { api.overlays().then((s) => setUnlocked(s.unlocked)).catch(() => setUnlocked(false)); }, []);
  const rule = `by dn.children="${st.container}" read`;
  if (unlocked === null) return null;
  if (!unlocked) {
    return (
      <div className="plan">
        <p>To let service accounts read, one clause has to be added to your catch-all access rule: <code>{rule}</code>, after the rule that protects <code>userPassword</code>. Dapper can do that for you once the server settings are unlocked (the separate configuration login) on the <a href="#/server" onClick={() => undefined}>Server</a> page. Come back and use <strong>Check again</strong>.</p>
        <button onClick={async () => { setUnlocked(null); try { setUnlocked((await api.overlays()).unlocked); } catch { setUnlocked(false); } }}>Check again</button>
      </div>
    );
  }
  return (
    <div className="plan">
      {!plan ? (
        <>
          <p>The server's access rules don't let service accounts read the directory. Dapper can add one clause to the catch-all rule to allow it, and keeps a copy of the current rules first.</p>
          {err && <Notice kind="error">{err}</Notice>}
          <button disabled={busy} onClick={async () => { setBusy(true); setErr(""); try { setPlan(await api.previewServer({ kind: "acl.service-read" })); } catch (x) { setErr((x as Error).message); } finally { setBusy(false); } }}>
            {busy ? "Working…" : "Show me the change"}</button>
        </>
      ) : (
        <>
          <ul>{plan.summary.map((s) => <li key={s}>{s}</li>)}</ul>
          {plan.warnings.map((w) => <Notice key={w} kind="warn">{w}</Notice>)}
          <details><summary>The exact change</summary><pre className="code">{plan.ldif}</pre></details>
          <label className="inline"><input type="checkbox" checked={ok} onChange={(e) => setOk(e.target.checked)} /> Yes, make this change</label>
          {err && <Notice kind="error">{err}</Notice>}
          <button className="primary" disabled={!ok || busy} onClick={async () => {
            setBusy(true); setErr("");
            try { await api.applyServer({ kind: "acl.service-read" }); setPlan(null); await onFixed(); } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
          }}>{busy ? "Applying…" : "Let service accounts read"}</button>
        </>
      )}
    </div>
  );
}
