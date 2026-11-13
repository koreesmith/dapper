import { useCallback, useEffect, useState } from "react";
import { api, MigCheck, MigStatus, MigSwitched } from "./api";
import { ConnectHelp } from "./Connect";
import { Notice, SecretBox } from "./ui";

const COMPOSE = "docker compose -f docker-compose.yml -f docker-compose.server.yml up -d --build";
const KIND = { people: "People", groups: "Groups", units: "Units", other: "Other entries" } as const;

/** Moves an existing directory into a directory server that Dapper runs. */
export function Migrate() {
  const [st, setSt] = useState<MigStatus | null>(null);
  const [err, setErr] = useState("");
  const [dn, setDn] = useState("");
  const [pw, setPw] = useState("");
  const [check, setCheck] = useState<MigCheck | null>(null);
  const [skip, setSkip] = useState(false);
  const [approveCopy, setApproveCopy] = useState(false);
  const [approveSwitch, setApproveSwitch] = useState(false);
  const [busy, setBusy] = useState("");
  const [done, setDone] = useState<MigSwitched | null>(null);

  const load = useCallback(() => api.migration().then(setSt).catch((x) => setErr((x as Error).message)), []);
  useEffect(() => { load(); }, [load]);
  // While a copy runs, look at its progress every second and a half.
  useEffect(() => {
    if (!st?.job.running) return;
    const t = setInterval(load, 1500);
    return () => clearInterval(t);
  }, [st?.job.running, load]);

  const run = async (name: string, fn: () => Promise<void>) => {
    setBusy(name); setErr("");
    try { await fn(); } catch (x) { setErr((x as Error).message); } finally { setBusy(""); }
  };
  const creds = { sourceDN: dn.trim(), sourcePassword: pw, skipUnsupported: skip };

  if (done) return <Switched r={done} />;
  if (!st) return <div className="page">{err ? <Notice kind="error">{err}</Notice> : "Loading…"}</div>;

  // After the switch: keep or undo it.
  if (st.canRollback) {
    return (
      <div className="page narrow">
        <h2>Migration</h2>
        <div className="panel">
          <h3>Dapper is using the new server</h3>
          <p>Dapper switched to the server it runs. The old server (<code>{st.previousURL}</code>) is untouched, and Dapper still remembers how to reach it so you can go back.</p>
          <p className="sub">Check that your machines and applications work with the new server. When you're happy, finish the migration: Dapper then forgets the old server's saved password.</p>
          {err && <Notice kind="error">{err}</Notice>}
          <div className="formactions">
            <button disabled={!!busy} onClick={() => { if (window.confirm("Go back to the old server? Everyone will be signed out.")) run("rollback", async () => { await api.migRollback(); window.location.hash = "/"; window.location.reload(); }); }}>
              {busy === "rollback" ? "Rolling back…" : "Roll back to the old server"}</button>
            <button className="primary" disabled={!!busy} onClick={() => { if (window.confirm("Finish the migration? Dapper forgets the old server's saved connection and you can't roll back.")) run("finish", async () => { await api.migFinish(); await load(); }); }}>
              {busy === "finish" ? "Finishing…" : "Finish the migration"}</button>
          </div>
        </div>
      </div>
    );
  }
  if (st.dapperRun) {
    return (
      <div className="page narrow">
        <h2>Migration</h2>
        <p>Dapper already runs this directory server, so there is nothing to migrate.</p>
      </div>
    );
  }

  const ns = st.newServer;
  const ready = !!ns?.found && !!ns.reachable;
  const job = st.job;
  const res = job.result;

  return (
    <div className="page narrow">
      <h2>Migrate to a server Dapper runs</h2>
      <p className="sub">Move your directory from <code>{st.sourceURL}</code> to a new OpenLDAP server that Dapper runs for you. Your old server is only read, never changed. The new server gets a copy with the same directory name, so every name, group and password stays valid, and you can undo the switch until you finish.</p>
      {err && <Notice kind="error">{err}</Notice>}

      <div className="panel">
        <h3>1. The new server</h3>
        {ready
          ? <Notice kind="ok">Found Dapper's new server and connected to it{ns?.host ? <> (it will be known to your machines as <strong>{ns.host}</strong>)</> : ""}.</Notice>
          : (
            <>
              <Notice kind="warn">{ns?.message ?? "Dapper can't find a new directory server."}</Notice>
              {ns?.reason === "already-used" || ns?.reason === "unusable"
                ? <p className="sub">Details are in <code>docs/migrating.md</code> (in your Dapper folder) under “If something goes wrong”.</p>
                : (
                  <>
                    <p>Start it next to Dapper (your Dapper settings and data are kept). Set <code>LDAP_HOSTNAME</code> in your <code>.env</code> first to the name your machines will use for it (see <code>.env.example</code>). If your old server runs on this same machine, also set <code>LDAP_PORT=3389</code> and <code>LDAPS_PORT=3636</code> so they don't clash. Then run this where Dapper is installed:</p>
                    <pre className="code">{COMPOSE}</pre>
                  </>
                )}
              <div className="formactions"><button onClick={load}>Check again</button></div>
            </>
          )}
      </div>

      <div className="panel">
        <h3>2. Check the old server</h3>
        <p className="sub">Dapper reads the whole directory to see what will fit. For passwords to be copied it needs the old server's <strong>directory manager</strong> (the root account, for example <code>cn=admin,dc=example,dc=com</code>). It is used for this and the copy only, and never saved. To also carry over features such as memberOf, unlock the server settings on the <a href="#/server">Server</a> page first.</p>
        <label>Directory manager<input value={dn} onChange={(e) => setDn(e.target.value)} placeholder="cn=admin,dc=example,dc=com" autoComplete="off" spellCheck={false} /></label>
        <label>Password<input type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoComplete="off" /></label>
        <div className="formactions">
          <button className="primary" disabled={!ready || !dn.trim() || !pw || !!busy} onClick={() => run("check", async () => { setCheck(await api.migCheck(creds)); setApproveCopy(false); })}>
            {busy === "check" ? "Checking…" : "Check the old server"}</button>
        </div>
        {check && <Report c={check} skip={skip} setSkip={setSkip} />}
      </div>

      <div className="panel">
        <h3>3. Copy</h3>
        <p className="sub">Creates the directory on the new server and copies everything, with passwords exactly as they are. It can be run again, so run it once more just before you switch to pick up changes made in the meantime. Nothing on the old server changes.</p>
        {check && check.blockers.length === 0 && (
          <label className="inline"><input type="checkbox" checked={approveCopy} onChange={(e) => setApproveCopy(e.target.checked)} /> Yes, copy {check.entries} entries to the new server</label>
        )}
        <div className="formactions">
          <button className="primary" disabled={!check || check.blockers.length > 0 || !approveCopy || job.running || !!busy || (check.affectedEntries > 0 && !skip)}
            onClick={() => run("copy", async () => { await api.migCopy(creds); await load(); })}>
            {job.running ? "Copying…" : res ? "Copy again" : "Copy now"}</button>
          {!check && <span className="sub">Check the old server first.</span>}
          {check && check.affectedEntries > 0 && !skip && <span className="sub">Choose to leave out the entries that don't fit, above.</span>}
        </div>
        {job.running && (
          <div role="status" aria-live="polite">
            <p>{job.phase}{job.total ? ` (${job.done} of ${job.total})` : "…"}</p>
            <progress max={job.total || 1} value={job.done} style={{ width: "100%" }} aria-label="Copy progress" />
          </div>
        )}
        {job.error && <Notice kind="error">{job.error}</Notice>}
        {res && !job.running && (
          <div>
            {res.verified
              ? <Notice kind="ok">The copy is finished and checked: every entry on the new server is identical to the old one.</Notice>
              : <Notice kind="error">The copy has problems ({res.failed} entries failed, {res.mismatched} differ). Fix them and copy again before switching.</Notice>}
            <ul>
              <li>Added {res.added}, updated {res.updated}, already the same {res.unchanged}.</li>
              {res.skipped > 0 && <li>Left out {res.skipped} entries that use schema the new server doesn't have.</li>}
              {res.carriedOverlays && res.carriedOverlays.length > 0 && <li>Features turned on at the new server: {res.carriedOverlays.join(", ")}.</li>}
              {res.extras > 0 && <li>{res.extras} entries exist only on the new server (nothing was deleted).</li>}
            </ul>
            {res.failures && res.failures.length > 0 && <details><summary>Entries that failed</summary><pre className="code">{res.failures.join("\n")}</pre></details>}
            {res.mismatchSample && res.mismatchSample.length > 0 && <details><summary>Entries that differ</summary><pre className="code">{res.mismatchSample.join("\n")}</pre></details>}
          </div>
        )}
      </div>

      <div className="panel">
        <h3>4. Switch Dapper to the new server</h3>
        <p className="sub">Dapper starts managing the new server and everyone is signed out (people sign in again with the same passwords). Dapper keeps the old server's connection so you can roll back. Moving your machines and applications over (DNS, certificates) is up to you; the next page lists exactly what.</p>
        <label className="inline"><input type="checkbox" checked={approveSwitch} onChange={(e) => setApproveSwitch(e.target.checked)} disabled={!st.readyToSwitch} /> Yes, switch Dapper to the new server</label>
        <div className="formactions">
          <button className="primary" disabled={!st.readyToSwitch || !approveSwitch || !!busy} onClick={() => run("switch", async () => { setDone(await api.migSwitch()); })}>
            {busy === "switch" ? "Switching…" : "Switch to the new server"}</button>
          {!st.readyToSwitch && <span className="sub">Available once a copy has finished and checked out.</span>}
        </div>
      </div>
    </div>
  );
}

function Report({ c, skip, setSkip }: { c: MigCheck; skip: boolean; setSkip: (b: boolean) => void }) {
  return (
    <div>
      {c.blockers.map((b) => <Notice key={b} kind="error">{b}</Notice>)}
      {c.warnings.map((w) => <Notice key={w} kind="warn">{w}</Notice>)}
      <h4>What Dapper found under <code>{c.suffix}</code></h4>
      <p>{c.entries} entries: {Object.entries(c.kinds).map(([k, n]) => `${n} ${(KIND[k as keyof typeof KIND] ?? k).toLowerCase()}`).join(", ")}.</p>
      {c.hashes.length > 0 && (
        <p>Passwords are stored as: {c.hashes.map((h) => `${h.scheme} (${h.count})${h.supported ? "" : " — the new server can't check these"}`).join(", ")}{c.plainText > 0 ? `, plain text (${c.plainText})` : ""}.</p>
      )}
      {c.carryOverlays.length > 0 && <p>Features Dapper will turn on at the new server: {c.carryOverlays.join(", ")}.</p>}
      {c.unsupported.length > 0 && (
        <>
          <h4>Doesn't fit the new server ({c.affectedEntries} entries)</h4>
          <table className="users" tabIndex={0} aria-label="Schema the new server doesn't know">
            <thead><tr><th scope="col">Kind</th><th scope="col">Name</th><th scope="col">Entries</th><th scope="col">For example</th></tr></thead>
            <tbody>{c.unsupported.slice(0, 20).map((u) => <tr key={u.kind + u.name}><td>{u.kind}</td><td>{u.name}</td><td>{u.count}</td><td>{u.sample[0]}</td></tr>)}</tbody>
          </table>
          <label className="inline"><input type="checkbox" checked={skip} onChange={(e) => setSkip(e.target.checked)} /> Leave out these {c.affectedEntries} entries and copy the rest</label>
        </>
      )}
      <details className="advanced">
        <summary>What isn't copied</summary>
        <ul>{c.notCarried.map((n) => <li key={n}>{n}</li>)}</ul>
      </details>
    </div>
  );
}

/** Shown once, after the switch: the new passwords and what to do next. */
function Switched({ r }: { r: MigSwitched }) {
  return (
    <div className="page narrow">
      <h2>Dapper is now using the new server</h2>
      <Notice kind="warn">Save these two passwords now. They are shown only this once. Dapper keeps the directory manager's password so it can work, but it never stores the configuration password.</Notice>
      <div className="panel">
        <h3>Directory manager</h3>
        <p className="sub">Full control of <code>{r.suffix}</code>. Sign in as <code>{r.managerDN}</code>.</p>
        <SecretBox value={r.managerPassword} />
        <h3>Configuration administrator</h3>
        <p className="sub">Needed for the Server page. Sign in as <code>{r.configDN}</code>.</p>
        <SecretBox value={r.configPassword} />
      </div>
      <div className="panel">
        <h3>Now move your machines over</h3>
        <ol>{r.checklist.map((c) => <li key={c}>{c}</li>)}</ol>
      </div>
      <div className="panel">
        <h3>Connecting your machines</h3>
        <ConnectHelp host={r.host || "localhost"} suffix={r.suffix} certificate={r.certificate} />
      </div>
      <p className="sub">Everyone has been signed out. Sign in again with your usual directory password; a <strong>Migration</strong> tab will let you roll back or finish.</p>
      <div className="formactions"><button className="primary" onClick={() => { window.location.hash = "/"; window.location.reload(); }}>Go to sign in</button></div>
    </div>
  );
}
