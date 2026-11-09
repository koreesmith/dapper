import { useEffect, useState } from "react";
import { api, ServerChange, ServerConfig, ServerPreview } from "./api";
import { Modal, Notice } from "./ui";

/** Previews a change to the directory server, asks for approval, applies it (after a backup), and reports back. */
function ChangeDialog({ title, change, onClose, onDone }: { title: string; change: ServerChange; onClose: () => void; onDone: (backup: string) => void }) {
  const [prev, setPrev] = useState<ServerPreview | null>(null);
  const [approved, setApproved] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => { api.previewServer(change).then(setPrev).catch((x) => setErr((x as Error).message)); }, [change]);

  return (
    <Modal title={title} onClose={onClose}>
      {prev && (
        <>
          <h4>What will happen</h4>
          <ul className="plainlist">{prev.summary.map((x) => <li key={x}>{x}</li>)}</ul>
          {prev.warnings.map((w) => <Notice key={w} kind="warn">{w}</Notice>)}
          <details className="advanced"><summary>Technical details</summary><pre className="code">{prev.ldif}</pre></details>
          <p className="sub">Dapper saves a copy of the server's current configuration first.</p>
          <label className="inline"><input type="checkbox" checked={approved} onChange={(e) => setApproved(e.target.checked)} /> Yes, make this change on my directory server</label>
        </>
      )}
      {err && <Notice kind="error">{err}</Notice>}
      <div className="formactions">
        <button className="primary" disabled={!prev || !approved || busy} onClick={async () => {
          setBusy(true); setErr("");
          try { const r = await api.applyServer(change); onDone(r.backup); } catch (x) { setErr((x as Error).message); setBusy(false); }
        }}>{busy ? "Applying…" : "Apply"}</button>
        <button onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}

/** Schemas, indexes, access rules, encryption and replication. Shown on the Server page once it is unlocked. */
export function ServerSettings({ onDone }: { onDone: (text: string, backup?: string) => void }) {
  const [cfg, setCfg] = useState<ServerConfig | null>(null);
  const [err, setErr] = useState("");
  const [change, setChange] = useState<{ title: string; c: ServerChange } | null>(null);
  const load = () => api.serverConfig().then((c) => { setCfg(c); setErr(""); }).catch((x) => setErr((x as Error).message));
  useEffect(() => { load(); }, []);

  if (!cfg) return err ? <Notice kind="error">{err}</Notice> : <p>Loading…</p>;
  const ask = (title: string, c: ServerChange) => setChange({ title, c });

  return (
    <>
      <h2>Other server settings</h2>
      <p className="sub">These change how the directory server itself behaves. Each change is previewed first and a copy of the current configuration is saved.</p>

      <Schemas cfg={cfg} ask={ask} />
      <Indexes cfg={cfg} ask={ask} />
      <AccessRules cfg={cfg} ask={ask} />
      <Encryption cfg={cfg} ask={ask} />
      <Replication cfg={cfg} />

      {change && (
        <ChangeDialog title={change.title} change={change.c} onClose={() => setChange(null)}
          onDone={(backup) => { setChange(null); onDone(change.title + ": done.", backup); load(); }} />
      )}
    </>
  );
}

type Ask = (title: string, c: ServerChange) => void;

function Schemas({ cfg, ask }: { cfg: ServerConfig; ask: Ask }) {
  return (
    <div className="panel">
      <h3>Schemas</h3>
      <p className="sub">Schemas tell the server which kinds of information it understands. These are loaded:</p>
      <ul className="chips">{cfg.schemas.map((s) => <li key={s}><span>{s}</span></li>)}</ul>
      {cfg.sshKeysSchema ? (
        <p><span className="badge on">SSH keys supported</span> The server understands SSH public keys, so Dapper can store them on Linux accounts.</p>
      ) : (
        <>
          <p><span className="badge off">SSH keys not supported yet</span> To keep people's SSH public keys in the directory the server needs the standard OpenSSH schema.</p>
          <button onClick={() => ask("Add SSH key support", { kind: "schema.sshkeys" })}>Add SSH key support…</button>
        </>
      )}
    </div>
  );
}

function Indexes({ cfg, ask }: { cfg: ServerConfig; ask: Ask }) {
  const [attr, setAttr] = useState("");
  const [types, setTypes] = useState<string[]>(["eq"]);
  const toggle = (t: string) => setTypes(types.includes(t) ? types.filter((x) => x !== t) : [...types, t]);
  return (
    <div className="panel">
      <h3>Search speed (indexes)</h3>
      <p className="sub">An index makes searches on that detail fast. Without one, the server reads every entry. Add one for anything applications look people up by.</p>
      <table className="table">
        <thead><tr><th scope="col">Detail</th><th scope="col">Speeds up</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead>
        <tbody>
          {cfg.indexes.map((i) => (
            <tr key={i.raw}>
              <td>{i.attrs.join(", ")}</td><td>{i.types.join(", ") || "—"}</td>
              <td><button onClick={() => ask(`Stop indexing ${i.attrs[0]}`, { kind: "index.remove", attr: i.attrs[0] })}>Remove…</button></td>
            </tr>
          ))}
        </tbody>
      </table>
      <form className="list-tools" onSubmit={(e) => { e.preventDefault(); ask(`Index ${attr}`, { kind: "index.add", attr: attr.trim(), types }); }}>
        <input aria-label="Detail to index" placeholder="Detail, e.g. mail" value={attr} onChange={(e) => setAttr(e.target.value)} />
        {[["eq", "exact"], ["sub", "contains"], ["pres", "exists"], ["approx", "sounds like"]].map(([t, l]) => (
          <label className="inline" key={t}><input type="checkbox" checked={types.includes(t)} onChange={() => toggle(t)} /> {l}</label>
        ))}
        <button className="primary" disabled={!attr.trim() || types.length === 0}>Add index…</button>
      </form>
    </div>
  );
}

const EXAMPLE_RULES = [
  "to attrs=userPassword by self write by anonymous auth by * none",
  "to * by self write by * read",
];

function AccessRules({ cfg, ask }: { cfg: ServerConfig; ask: Ask }) {
  const [edit, setEdit] = useState(false);
  const [text, setText] = useState("");
  return (
    <div className="panel">
      <h3>Who can read and change what (access rules)</h3>
      <p className="sub">Rules are checked from the top; the first that matches decides. {cfg.rootDN && <>The directory manager (<code>{cfg.rootDN}</code>) is never blocked by these.</>}</p>
      <ol className="plainlist">{cfg.acl.map((r) => <li key={r}><code>{r}</code></li>)}</ol>
      {!edit ? (
        <button onClick={() => { setText(cfg.acl.join("\n")); setEdit(true); }}>Edit rules…</button>
      ) : (
        <>
          <Notice kind="warn">Careful: a mistake here can stop people, applications and even Dapper from using the directory. Dapper checks the rules' shape, not their meaning. You will see a preview and a backup is saved, but fixing a bad rule set may need access to the server itself.</Notice>
          <label>One rule per line
            <textarea rows={8} value={text} spellCheck={false} onChange={(e) => setText(e.target.value)} style={{ fontFamily: "monospace" }} />
          </label>
          <details className="advanced"><summary>Examples</summary>
            <p className="sub">Rules read “to <em>what</em> by <em>who</em> <em>access</em>”. Click to add a line.</p>
            {EXAMPLE_RULES.map((r) => <p key={r}><button type="button" className="link" onClick={() => setText(text.trimEnd() + "\n" + r)}>{r}</button></p>)}
          </details>
          <div className="formactions">
            <button className="primary" onClick={() => ask("Replace access rules", { kind: "acl.set", rules: text.split("\n") })}>Review the change…</button>
            <button onClick={() => setEdit(false)}>Cancel</button>
          </div>
        </>
      )}
    </div>
  );
}

function Encryption({ cfg, ask }: { cfg: ServerConfig; ask: Ask }) {
  const t = cfg.tls;
  const [edit, setEdit] = useState(false);
  const [cert, setCert] = useState(t.cert);
  const [key, setKey] = useState(t.key);
  const [ca, setCa] = useState(t.ca);
  const c = t.served;
  return (
    <div className="panel">
      <h3>Encryption (TLS certificate)</h3>
      {c ? (
        <>
          <p>
            {c.expired ? <span className="badge off">Expired</span> : c.daysLeft < 30 ? <span className="badge off">Expires soon</span> : <span className="badge on">Valid</span>}{" "}
            The server presents a certificate for <strong>{c.names.join(", ") || c.subject}</strong>, {c.expired ? `which expired on ${c.notAfter}` : `valid until ${c.notAfter} (${c.daysLeft} days)`}.
          </p>
          <p className="sub">Issued by {c.issuer}{c.selfSigned ? " (self-signed: applications will need to be told to trust it)" : ""}.</p>
        </>
      ) : <Notice kind="warn">Couldn't read the server's certificate: {t.servedError}</Notice>}
      <p className="sub">Files the server is set to use: certificate <code>{t.cert || "not set"}</code>, key <code>{t.key || "not set"}</code>, authority <code>{t.ca || "not set"}</code>.</p>
      {!edit ? <button onClick={() => setEdit(true)}>Change certificate files…</button> : (
        <>
          <Notice kind="warn">These are paths on the directory server. If they are wrong, encrypted connections can stop working.</Notice>
          <label>Certificate file<input value={cert} onChange={(e) => setCert(e.target.value)} placeholder="/etc/openldap/certs/server.crt" /></label>
          <label>Private key file<input value={key} onChange={(e) => setKey(e.target.value)} placeholder="/etc/openldap/certs/server.key" /></label>
          <label>Certificate authority file (optional)<input value={ca} onChange={(e) => setCa(e.target.value)} placeholder="/etc/openldap/certs/ca.crt" /></label>
          <div className="formactions">
            <button className="primary" onClick={() => ask("Change certificate files", { kind: "tls.set", cert, key, ca })}>Review the change…</button>
            <button onClick={() => setEdit(false)}>Cancel</button>
          </div>
        </>
      )}
    </div>
  );
}

function Replication({ cfg }: { cfg: ServerConfig }) {
  const r = cfg.replication;
  const nothing = r.syncrepl.length === 0 && !r.provider && r.serverID.length === 0;
  return (
    <div className="panel">
      <h3>Replication (copies of this server)</h3>
      <p className="sub">This is read-only. To let other servers copy this one, turn on “Allow this server to be copied” above.</p>
      {nothing && <p>Replication isn't set up on this server.</p>}
      {r.provider && <p><span className="badge on">Providing</span> Other servers can copy this one.</p>}
      {r.syncrepl.length > 0 && <><p><span className="badge on">Copying</span> This server copies from:</p><ul className="plainlist">{r.syncrepl.map((x) => <li key={x}><code>{x}</code></li>)}</ul></>}
      {r.mirrorMode && <p>Mirror mode is on: this server and its partner both accept changes.</p>}
      {r.serverID.length > 0 && <p className="sub">Server ID: {r.serverID.join(", ")}</p>}
      {r.contextCSN.length > 0 && <details className="advanced"><summary>How current this copy is</summary><ul className="plainlist">{r.contextCSN.map((x) => <li key={x}><code>{x}</code></li>)}</ul><p className="sub">Compare these between servers: they should match.</p></details>}
    </div>
  );
}
