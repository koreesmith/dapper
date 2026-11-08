import { useEffect, useState } from "react";
import { api, GIDFix, GIDIssue, GIDPreview, PersonGIDPreview } from "./api";
import { Modal, Notice } from "./ui";

function Summary({ p }: { p: GIDPreview }) {
  return (
    <>
      {p.conflict && <Notice kind="error">{p.conflict}</Notice>}
      {p.moving.length > 0 && <p><strong>{p.moving.length}</strong> {p.moving.length === 1 ? "person has" : "people have"} this as their primary group: {p.moving.map((m) => m.username).join(", ")}.</p>}
      {p.adopted.length > 0 && <p>Already using the number {p.new}: {p.adopted.map((m) => m.username).join(", ")}. They will belong to this group.</p>}
      {p.warnings.map((w) => <Notice key={w} kind="warn">{w}</Notice>)}
      {p.chgrpHint && (
        <details className="advanced"><summary>If you also want files on your servers to follow</summary>
          <p className="sub">On each server, something like this moves files from the old number to the new one (check it first):</p>
          <pre className="code">{p.chgrpHint}</pre>
        </details>
      )}
    </>
  );
}

/** Change one Linux group's number, previewing who is affected first. */
export function ChangeGroupID({ dn, name, current, onClose, onDone }: { dn: string; name: string; current: string; onClose: () => void; onDone: (msg: string) => void }) {
  const [gid, setGid] = useState(current);
  const [move, setMove] = useState(true);
  const [prev, setPrev] = useState<GIDPreview | null>(null);
  const [approved, setApproved] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  const review = async () => {
    setBusy(true); setErr("");
    try { setPrev(await api.groupGID(dn, gid.trim(), move)); setApproved(false); } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
  };
  const apply = async () => {
    setBusy(true); setErr("");
    try {
      const r = await api.groupGID(dn, gid.trim(), move, true);
      onDone(`${name} now has the group ID ${gid.trim()}${r.moved ? ` and ${r.moved} ${r.moved === 1 ? "person was" : "people were"} updated to match` : ""}.`);
    } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
  };

  return (
    <Modal title={`Change the group ID of ${name}`} onClose={onClose}>
      {!prev ? (
        <>
          <p>The group ID is the number Linux systems use for this group. It's currently <strong>{current}</strong>.</p>
          <label>New group ID<input value={gid} inputMode="numeric" onChange={(e) => setGid(e.target.value.replace(/\D/g, ""))} autoFocus /></label>
          <label className="inline"><input type="checkbox" checked={move} onChange={(e) => setMove(e.target.checked)} /> Also update the primary group ID of anyone who has this group as their primary group</label>
          {err && <Notice kind="error">{err}</Notice>}
          <div className="formactions">
            <button className="primary" disabled={busy || !gid.trim() || gid.trim() === current} onClick={review}>{busy ? "Checking…" : "Review the change"}</button>
            <button onClick={onClose}>Cancel</button>
          </div>
        </>
      ) : (
        <>
          <p>Change <strong>{prev.group}</strong> from {prev.old} to <strong>{prev.new}</strong>.</p>
          <Summary p={prev} />
          {err && <Notice kind="error">{err}</Notice>}
          <label className="inline"><input type="checkbox" checked={approved} disabled={!!prev.conflict} onChange={(e) => setApproved(e.target.checked)} /> Yes, make this change</label>
          <div className="formactions">
            <button onClick={() => { setPrev(null); setErr(""); }}>Back</button>
            <button className="primary" disabled={!approved || busy || !!prev.conflict} onClick={apply}>{busy ? "Applying…" : "Change the ID"}</button>
          </div>
        </>
      )}
    </Modal>
  );
}

function FixDialog({ fix, onClose, onDone }: { fix: GIDFix; onClose: () => void; onDone: (msg: string) => void }) {
  const [gp, setGp] = useState<GIDPreview | null>(null);
  const [pp, setPp] = useState<PersonGIDPreview | null>(null);
  const [approved, setApproved] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  // Preview what the fix would do as soon as the dialog opens.
  useEffect(() => {
    (async () => {
      try {
        if (fix.kind === "groupGID") setGp(await api.groupGID(fix.groupDN!, fix.gid, false));
        if (fix.kind === "personGID") setPp(await api.personGID(fix.personDN!, fix.gid));
      } catch (x) { setErr((x as Error).message); }
    })();
  }, [fix]);

  const apply = async () => {
    setBusy(true); setErr("");
    try {
      if (fix.kind === "groupGID") await api.groupGID(fix.groupDN!, fix.gid, false, true);
      else if (fix.kind === "personGID") await api.personGID(fix.personDN!, fix.gid, true);
      else await api.createLinuxGroup(fix.groupName!, fix.gid, true);
      onDone("Fixed: " + fix.label + ".");
    } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
  };

  return (
    <Modal title="Fix this?" onClose={onClose}>
      <p><strong>{fix.label}</strong></p>
      {gp && <Summary p={gp} />}
      {pp && pp.warnings.map((w) => <Notice key={w} kind="warn">{w}</Notice>)}
      {pp && <p>{pp.person}'s primary group ID changes from {pp.old} to {pp.new}, which is the group “{pp.group}”.</p>}
      {fix.kind === "createGroup" && <p>A new Linux group will be created in your groups container. {fix.groupName}'s account already points at this number, so nothing else changes.</p>}
      {err && <Notice kind="error">{err}</Notice>}
      <label className="inline"><input type="checkbox" checked={approved} onChange={(e) => setApproved(e.target.checked)} /> Yes, make this change</label>
      <div className="formactions">
        <button className="primary" disabled={!approved || busy || !!gp?.conflict} onClick={apply}>{busy ? "Applying…" : "Fix it"}</button>
        <button onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}

/** Finds group-number mismatches and offers one-click fixes. */
export function GroupChecker() {
  const [issues, setIssues] = useState<GIDIssue[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [fix, setFix] = useState<GIDFix | null>(null);
  const [msg, setMsg] = useState("");

  const run = async () => {
    setBusy(true); setErr("");
    try { setIssues((await api.checkGroups()).issues); } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
  };

  return (
    <div className="panel">
      <div className="pagehead">
        <div><h3>Check group numbers</h3>
          <p className="sub">Looks for people whose primary group doesn't match a group, personal groups whose number differs from the person's, and groups that share a number.</p></div>
        <button onClick={run} disabled={busy}>{busy ? "Checking…" : issues ? "Check again" : "Check for problems"}</button>
      </div>
      {msg && <Notice kind="ok">{msg}</Notice>}
      {err && <Notice kind="error">{err}</Notice>}
      {issues && issues.length === 0 && <Notice kind="ok">No mismatches found.</Notice>}
      {issues && issues.length > 0 && (
        <>
          <p className="sub">{issues.length} {issues.length === 1 ? "problem" : "problems"} found.</p>
          <ul className="issues">
            {issues.map((i, n) => (
              <li key={n}>
                <strong>{i.title}</strong>
                <div className="sub">{i.detail}</div>
                <div className="formactions">
                  {i.fixes.map((f) => <button key={f.label} type="button" onClick={() => setFix(f)}>{f.label}</button>)}
                </div>
              </li>
            ))}
          </ul>
        </>
      )}
      {fix && <FixDialog fix={fix} onClose={() => setFix(null)} onDone={(m) => { setFix(null); setMsg(m); run(); }} />}
    </div>
  );
}
