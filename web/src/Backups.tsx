import { useCallback, useEffect, useRef, useState } from "react";
import { api, BackupsState } from "./api";
import { Notice } from "./ui";

const COMPOSE = "docker compose -f docker-compose.yml -f docker-compose.server.yml";
const KIND = { scheduled: "Scheduled", manual: "Made on request", "pre-restore": "Saved before a restore" } as const;

const size = (n: number) => n >= 1 << 30 ? `${(n / (1 << 30)).toFixed(1)} GB` : n >= 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`;
const when = (iso: string) => new Date(iso).toLocaleString();

/** Backups of a Dapper-run directory server: list, back up now, download, and how to restore. */
export function Backups({ unlocked }: { unlocked: boolean }) {
  const [st, setSt] = useState<BackupsState | null>(null);
  const [err, setErr] = useState("");
  const [waiting, setWaiting] = useState(false);
  const [pick, setPick] = useState("");
  const base = useRef<string>("");

  const load = useCallback(() => api.backups().then(setSt).catch((x) => setErr((x as Error).message)), []);
  useEffect(() => { load(); }, [load]);

  // After "Back up now", check every couple of seconds until a newer backup shows up (or give up).
  useEffect(() => {
    if (!waiting) return;
    let n = 0;
    const t = setInterval(async () => {
      n++;
      try {
        const s = await api.backups();
        setSt(s);
        const newest = s.backups?.find((b) => b.kind !== "pre-restore")?.name ?? "";
        if (newest && newest !== base.current) setWaiting(false);
        else if (s.problem && s.status?.lastError && !s.requested) setWaiting(false);
      } catch { /* try again */ }
      if (n > 30) setWaiting(false);
    }, 2000);
    return () => clearInterval(t);
  }, [waiting]);

  if (!st) return err ? <Notice kind="error">{err}</Notice> : null;
  if (!st.enabled) {
    return (
      <details className="panel" id="backups" open>
        <summary><strong>Backups</strong></summary>
        {st.message
          ? <Notice kind="warn">{st.message}</Notice>
          : (
            <>
              <p>Dapper makes, lists and restores backups only for a directory server that <strong>Dapper itself runs</strong>: the one it
                sets up when you choose “I need a new one” in setup.</p>
              <p className="sub">Your directory server is one you run yourself, so back it up with your own tools. For OpenLDAP, <code>slapcat</code> on the server
                writes everything out as LDIF (<code>sudo slapcat -n 0 &gt; config.ldif</code> and <code>sudo slapcat -b &lt;your base DN&gt; &gt; data.ldif</code>),
                and Dapper's own settings are in its data volume (see “Upgrading and operating Dapper” in the docs).</p>
            </>
          )}
      </details>
    );
  }

  const list = st.backups ?? [];
  const s = st.status;
  const chosen = pick || list.find((b) => b.kind !== "pre-restore")?.name || list[0]?.name || "";

  return (
    <details className="panel" open id="backups">
      <summary><strong>Backups</strong></summary>
      <p className="sub">The server makes a backup of its configuration and all its people and groups{s?.intervalHours ? ` every ${s.intervalHours === 24 ? "day" : s.intervalHours + " hours"}` : ""} and keeps the newest {s?.keep ?? 14}. Backups hold every password hash, so keep copies somewhere safe and private.</p>
      {st.problem && <Notice kind="warn">{st.problem}</Notice>}
      {err && <Notice kind="error">{err}</Notice>}
      {s && (
        <p className="sub">
          {s.lastSuccess ? <>Last successful backup: {when(s.lastSuccess)}. </> : null}
          Directory data: {size(s.dataBytes)}. Free disk space: {size(s.diskFreeBytes)}.
        </p>
      )}
      <div className="formactions">
        <button type="button" className="primary" disabled={waiting} onClick={async () => {
          setErr("");
          base.current = list.find((b) => b.kind !== "pre-restore")?.name ?? "";
          try { await api.backupNow(); setWaiting(true); } catch (x) { setErr((x as Error).message); }
        }}>{waiting ? "Backing up…" : "Back up now"}</button>
      </div>

      {list.length === 0 ? <p>No backups yet.</p> : (
        <div>
          <table className="users" tabIndex={0} aria-label="Backups">
            <thead><tr><th scope="col">Made</th><th scope="col">Kind</th><th scope="col">Size</th><th scope="col"><span className="sr-only">Download</span></th></tr></thead>
            <tbody>
              {list.map((b) => (
                <tr key={b.name}>
                  <td>{when(b.time)}</td><td>{KIND[b.kind]}</td><td>{size(b.size)}</td>
                  <td>{unlocked ? <a href={api.backupUrl(b.name)}>Download</a> : <span className="sub">Unlock to download</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {!unlocked && list.length > 0 && <p className="sub">Downloading a backup needs the server settings unlocked (below), because it contains every password hash.</p>}

      {list.length > 0 && (
        <details className="advanced">
          <summary>Restore from a backup</summary>
          <p>Restoring puts the directory back exactly as it was when the backup was made. Anything changed since then (new people, changed passwords) is lost. People can't sign in while it runs, which takes a minute or so. Dapper can't stop the server itself, so you run three commands where Dapper is installed:</p>
          <label>Backup to restore
            <select value={chosen} onChange={(e) => setPick(e.target.value)}>
              {list.map((b) => <option key={b.name} value={b.name}>{when(b.time)} · {KIND[b.kind]}</option>)}
            </select>
          </label>
          <pre className="code">{`${COMPOSE} stop openldap
${COMPOSE} run --rm openldap restore ${chosen}
${COMPOSE} up -d`}</pre>
          <p className="sub">Before it replaces anything, the restore saves the current state as another backup (“saved before a restore”), so you can undo it the same way. The server's certificate isn't touched.</p>
        </details>
      )}
    </details>
  );
}
