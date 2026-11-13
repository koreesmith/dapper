import { useEffect, useState } from "react";
import { api, ApiError, ConnectInfo, OverlayParam, OverlayPreview, OverlayState, OverlayView } from "./api";
import { Modal, Notice } from "./ui";
import { ServerSettings } from "./ServerSettings";
import { ConnectHelp } from "./Connect";
import { Backups } from "./Backups";

const HELP_LDIF = `dn: olcDatabase={0}config,cn=config
changetype: modify
replace: olcRootDN
olcRootDN: cn=admin,cn=config
-
replace: olcRootPW
olcRootPW: {SSHA}PASTE-A-HASH-FROM-slappasswd-HERE`;

// For a server Dapper runs: its configuration login is cn=config-admin,cn=config, so only the password is replaced.
const DAPPER_RUN_HELP = `C="docker compose -f docker-compose.yml -f docker-compose.server.yml"
HASH=$($C exec -T openldap slappasswd -s 'a-new-long-password' | tr -d '\\r\\n')
$C exec -T openldap ldapmodify -Q -Y EXTERNAL -H "ldapi://%2Frun%2Fslapd%2Fldapi" <<EOF
dn: olcDatabase={0}config,cn=config
changetype: modify
replace: olcRootPW
olcRootPW: $HASH
EOF`;

export function Server() {
  const [state, setState] = useState<OverlayState | null>(null);
  const [err, setErr] = useState("");
  const [dn, setDn] = useState("cn=admin,cn=config");
  const [pw, setPw] = useState("");
  const [busy, setBusy] = useState(false);
  const [dialog, setDialog] = useState<{ o: OverlayView; action: "enable" | "update" | "disable" } | null>(null);
  const [done, setDone] = useState<{ text: string; backup?: string } | null>(null);

  const [connect, setConnect] = useState<ConnectInfo | null>(null);
  useEffect(() => {
    api.connectInfo().then((c) => {
      setConnect(c);
      if (c.dapperRun) setDn("cn=config-admin,cn=config"); // the login a Dapper-run server is created with
    }).catch(() => setConnect(null));
  }, []);

  const load = () => api.overlays().then((d) => { setState(d); if (d.unlockError) setErr(d.unlockError); })
    .catch((x) => setErr((x as Error).message));
  useEffect(() => { load(); }, []);

  if (!state) return <div className="page">{err ? <Notice kind="error">{err}</Notice> : "Loading…"}</div>;

  const unlock = async (e: React.FormEvent) => {
    e.preventDefault(); setBusy(true); setErr("");
    try { await api.unlockOverlays(dn, pw); setPw(""); await load(); } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
  };

  return (
    <div className="page narrow">
      <h2>Server features</h2>
      <p className="sub">Switch on directory server features such as group membership lists and password rules with a few clicks. Every change is explained and previewed first, and a copy of the server's current configuration is saved before anything changes.</p>
      {connect?.dapperRun && connect.host && connect.suffix && (
        <details className="panel" open>
          <summary><strong>Connect your machines</strong></summary>
          <p className="sub">What Linux servers and applications need to use this directory server.</p>
          <ConnectHelp host={connect.host} suffix={connect.suffix} certificate={connect.certificate} cert={connect.cert} ldapPort={connect.ldapPort} ldapsPort={connect.ldapsPort} />
        </details>
      )}
      <Backups unlocked={state.unlocked} />
      {done && <Notice kind="ok">{done.text}{done.backup && <> A copy of the previous configuration was saved (<a href={`/api/admin/overlays/backup?name=${encodeURIComponent(done.backup)}`}>download {done.backup}</a>).</>}</Notice>}

      {state.unlocked ? (
        <div className="panel row">
          <span><span className="badge on">Unlocked</span> You can make changes now. It locks again after 15 idle minutes.</span>
          <button onClick={async () => { await api.lockOverlays(); setDone(null); load(); }}>Lock</button>
        </div>
      ) : (
        <form className="panel" onSubmit={unlock}>
          <h3>Unlock server settings</h3>
          <p className="sub">Changing these features means changing the directory server's own configuration, which needs a separate administrator login (not the one you sign in to Dapper with). Dapper keeps it in memory for this visit only and never saves it.</p>
          <label>Configuration administrator<input value={dn} onChange={(e) => setDn(e.target.value)} autoComplete="off" /></label>
          <label>Password<input type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="current-password" /></label>
          {err && <Notice kind="error">{err}</Notice>}
          <div className="formactions"><button className="primary" disabled={busy || !dn || !pw}>{busy ? "Unlocking…" : "Unlock"}</button></div>
          <details className="advanced">
            <summary>I don't have this login</summary>
            <p className="sub">You have two options.</p>
            {connect?.dapperRun ? (
              <>
                <p><strong>1. Set a new password.</strong> The login on a server Dapper runs is <code>cn=config-admin,cn=config</code>. Dapper never stores its password, but the server lets you set a new one from inside its container. Run this where Dapper is installed (put your own password in place of <code>a-new-long-password</code>), then unlock with <code>cn=config-admin,cn=config</code> and that password:</p>
                <pre className="code">{DAPPER_RUN_HELP}</pre>
              </>
            ) : (
              <>
                <p><strong>1. Create the login.</strong> On the directory server, run <code>slappasswd</code> to make a password hash. Put the hash in the text below (replacing the placeholder), save it as <code>config.ldif</code>, and apply it with the command in the second box. Copy only what is inside the boxes.</p>
                <pre className="code">{HELP_LDIF}</pre>
                <pre className="code">sudo ldapmodify -Y EXTERNAL -H ldapi:/// -f config.ldif</pre>
              </>
            )}
            <p><strong>2. Don't unlock at all.</strong> Pick a feature below and choose <em>Show me the commands</em>: Dapper will write out exactly what to run on the server, and you run it yourself.</p>
          </details>
        </form>
      )}

      {state.overlays.map((o) => (
        <div className="panel overlay" key={o.id}>
          <div className="pagehead">
            <h3>{o.name}</h3>
            {o.enabled === null ? <span className="badge">Unknown until unlocked</span> : o.enabled ? <span className="badge on">On</span> : <span className="badge off">Off</span>}
          </div>
          <p>{o.summary}</p>
          <p className="sub"><strong>Why you'd want it:</strong> {o.why}</p>
          {o.notes?.map((n) => <Notice key={n} kind="warn">{n}</Notice>)}
          <div className="formactions">
            {o.enabled ? (
              <>
                <button onClick={() => setDialog({ o, action: "update" })}>Change…</button>
                <button onClick={() => setDialog({ o, action: "disable" })}>Turn off…</button>
                {o.id === "memberof" && <RefreshButton onDone={(n) => setDone({ text: `Refreshed ${n} group${n === 1 ? "" : "s"}.` })} />}
              </>
            ) : (
              <button className="primary" onClick={() => setDialog({ o, action: "enable" })}>{state.unlocked ? "Turn on…" : "Show me how to turn this on…"}</button>
            )}
          </div>
        </div>
      ))}

      {state.unlocked && <ServerSettings onDone={(text, backup) => { setDone({ text, backup }); window.scrollTo({ top: 0 }); }} />}

      {dialog && (
        <OverlayDialog state={state} o={dialog.o} action={dialog.action} onClose={() => setDialog(null)}
          onDone={(text, backup) => { setDialog(null); setDone({ text, backup }); load(); }} />
      )}
    </div>
  );
}

function RefreshButton({ onDone }: { onDone: (n: number) => void }) {
  const [ask, setAsk] = useState(false);
  const [err, setErr] = useState("");
  return (
    <>
      <button onClick={() => setAsk(true)}>Refresh existing groups…</button>
      {ask && (
        <Modal title="Refresh existing groups?" onClose={() => setAsk(false)}>
          <p>memberOf only notices changes made after it was switched on. To fill it in for groups that already have members, Dapper re-saves each group's member list (the same people, in one safe step per group). Nobody is added or removed.</p>
          {err && <Notice kind="error">{err}</Notice>}
          <div className="formactions">
            <button className="primary" onClick={async () => {
              try { const r = await api.refreshMemberOf(); setAsk(false); onDone(r.refreshedGroups); } catch (x) { setErr((x as Error).message); }
            }}>Refresh groups</button>
            <button onClick={() => setAsk(false)}>Cancel</button>
          </div>
        </Modal>
      )}
    </>
  );
}

function ParamField({ p, value, onChange }: { p: OverlayParam; value: string; onChange: (v: string) => void }) {
  if (p.type === "bool") {
    return (
      <label className="inline"><input type="checkbox" checked={value === "true"} onChange={(e) => onChange(e.target.checked ? "true" : "false")} /> {p.label}</label>
    );
  }
  if (p.type === "multi") {
    const chosen = value.split(",").filter(Boolean);
    return (
      <fieldset>
        <legend>{p.label}</legend>
        {p.options?.map((o) => (
          <label className="inline" key={o.value}>
            <input type="checkbox" checked={chosen.includes(o.value)}
              onChange={(e) => onChange((e.target.checked ? [...chosen, o.value] : chosen.filter((x) => x !== o.value)).join(","))} /> {o.label}
          </label>
        ))}
      </fieldset>
    );
  }
  return (
    <label>{p.label}
      <input type={p.type === "number" ? "number" : "text"} value={value} onChange={(e) => onChange(e.target.value)} />
      {p.hint && <span className="sub">{p.hint}</span>}
    </label>
  );
}

function CopyBlock({ label, text }: { label: string; text: string }) {
  const [copied, setCopied] = useState(false);
  if (!text) return null;
  return (
    <div>
      <div className="copyhead"><strong>{label}</strong>
        <button type="button" className="link" onClick={async () => { try { await navigator.clipboard.writeText(text); setCopied(true); setTimeout(() => setCopied(false), 1500); } catch { /* blocked */ } }}>{copied ? "Copied" : "Copy"}</button>
      </div>
      <pre className="code">{text}</pre>
    </div>
  );
}

function OverlayDialog({ state, o, action, onClose, onDone }: {
  state: OverlayState; o: OverlayView; action: "enable" | "update" | "disable"; onClose: () => void; onDone: (text: string, backup?: string) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>(() => {
    const v: Record<string, string> = {};
    for (const p of o.params) v[p.key] = o.values[p.key] ?? p.default;
    return v;
  });
  const [dbDN, setDbDN] = useState("olcDatabase={1}mdb,cn=config");
  const [preview, setPreview] = useState<OverlayPreview | null>(null);
  const [approved, setApproved] = useState(false);
  const [refresh, setRefresh] = useState(true);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const req = (confirmed = false) => ({ overlay: o.id, action, values, dbDN, confirmed, refresh });
  const review = async () => {
    setBusy(true); setErr("");
    try { setPreview(await api.previewOverlay(req())); setApproved(false); } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
  };
  const apply = async () => {
    setBusy(true); setErr("");
    try {
      const r = await api.applyOverlay(req(true));
      onDone(`${o.name}: done.${r.refreshedGroups ? ` Refreshed ${r.refreshedGroups} groups.` : ""}`, r.backup);
    } catch (x) {
      setErr((x as Error).message);
      const b = (x as ApiError).data as { backup?: string } | undefined;
      void b;
    } finally { setBusy(false); }
  };

  const title = action === "disable" ? `Turn off ${o.name}?` : action === "update" ? `Change ${o.name}` : `Turn on ${o.name}`;
  const normal = o.params.filter((p) => !p.advanced), advanced = o.params.filter((p) => p.advanced);

  return (
    <Modal title={title} onClose={onClose}>
      {!preview ? (
        <>
          {action !== "disable" ? (
            <>
              {normal.map((p) => <ParamField key={p.key} p={p} value={values[p.key] ?? ""} onChange={(v) => setValues({ ...values, [p.key]: v })} />)}
              {advanced.length > 0 && (
                <details className="advanced"><summary>Advanced options</summary>
                  {advanced.map((p) => <ParamField key={p.key} p={p} value={values[p.key] ?? ""} onChange={(v) => setValues({ ...values, [p.key]: v })} />)}
                </details>
              )}
            </>
          ) : <p>{o.summary}</p>}
          {!state.unlocked && (
            <details className="advanced"><summary>Which database holds your directory?</summary>
              <p className="sub">Run this on the server to find it, and paste the first line it prints:</p>
              <pre className="code">{`sudo ldapsearch -Y EXTERNAL -H ldapi:/// -b cn=config -LLL "(olcSuffix=${state.suffix})" dn`}</pre>
              <label>Database<input value={dbDN} onChange={(e) => setDbDN(e.target.value)} /></label>
            </details>
          )}
          {err && <Notice kind="error">{err}</Notice>}
          <div className="formactions">
            <button className="primary" disabled={busy} onClick={review}>{busy ? "Working…" : "Review the changes"}</button>
            <button onClick={onClose}>Cancel</button>
          </div>
        </>
      ) : (
        <>
          <h4>What will happen</h4>
          <ul className="plainlist">{(preview.summary ?? []).map((x) => <li key={x}>{x}</li>)}</ul>
          {(preview.warnings ?? []).map((w) => <Notice key={w} kind="warn">{w}</Notice>)}
          {o.id === "memberof" && action === "enable" && preview.unlocked && (
            <label className="inline"><input type="checkbox" checked={refresh} onChange={(e) => setRefresh(e.target.checked)} /> Also refresh existing groups now, so current members show memberOf</label>
          )}
          <details className="advanced"><summary>Technical details</summary>
            <CopyBlock label="Server configuration change (LDIF)" text={preview.ldif} />
            <CopyBlock label="Directory change (LDIF)" text={preview.dataLDIF} />
          </details>
          {preview.unlocked ? (
            <>
              <p className="sub">Dapper saves a copy of the server's current configuration first.</p>
              <label className="inline"><input type="checkbox" checked={approved} onChange={(e) => setApproved(e.target.checked)} /> Yes, make these changes on my directory server</label>
              {err && <Notice kind="error">{err}</Notice>}
              <div className="formactions">
                <button onClick={() => { setPreview(null); setErr(""); }}>Back</button>
                <button className="primary" disabled={!approved || busy} onClick={apply}>{busy ? "Applying…" : "Apply"}</button>
              </div>
            </>
          ) : (
            <>
              <p>The server's configuration is locked, so Dapper can't make this change itself. To do it yourself:</p>
              <ol className="plainlist">
                {preview.commands.data && <li>Save the <em>Directory change</em> above as <code>dapper-directory-change.ldif</code> and run <code>{preview.commands.data}</code></li>}
                {preview.commands.config && <li>Save the <em>Server configuration change</em> above as <code>dapper-server-change.ldif</code> and run <code>{preview.commands.config}</code> on the directory server.</li>}
              </ol>
              <div className="formactions"><button onClick={() => setPreview(null)}>Back</button><button onClick={onClose}>Close</button></div>
            </>
          )}
        </>
      )}
    </Modal>
  );
}
