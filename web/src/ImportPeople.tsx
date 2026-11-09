import { useEffect, useState } from "react";
import { api, GroupSummary, ImportResult } from "./api";
import { Notice } from "./ui";

const TEMPLATE = "first name,last name,email,phone,username\nAda,Lovelace,ada@example.org,+1 555 0100,\n";

function download(name: string, text: string) {
  const a = document.createElement("a");
  a.href = URL.createObjectURL(new Blob([text], { type: "text/csv" }));
  a.download = name;
  a.click();
  URL.revokeObjectURL(a.href);
}
const csvCell = (v: string) => (/[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v);

export function ImportPeople() {
  const [csv, setCsv] = useState("");
  const [groups, setGroups] = useState<GroupSummary[]>([]);
  const [chosen, setChosen] = useState<string[]>([]);
  const [check, setCheck] = useState<ImportResult | null>(null);
  const [done, setDone] = useState<ImportResult | null>(null);
  const [approved, setApproved] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => { api.assignableGroups().then(setGroups).catch(() => {}); }, []);

  const run = async (dry: boolean) => {
    setBusy(true); setErr("");
    try {
      const r = await api.importUsers(csv, dry, chosen);
      if (dry) { setCheck(r); setApproved(false); } else { setDone(r); setCheck(null); }
    } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
  };

  if (done) {
    const withPw = done.rows.filter((r) => r.temporaryPassword);
    return (
      <div className="page">
        <p><a href="#/people">← Back to people</a></p>
        <h2>Import finished</h2>
        <Notice kind={done.failed ? "warn" : "ok"}>{done.created} {done.created === 1 ? "person was" : "people were"} added{done.failed ? `; ${done.failed} couldn't be` : ""}.</Notice>
        {withPw.length > 0 && (
          <div className="panel">
            <h3>Temporary passwords</h3>
            <p>Each new person has a temporary password. <strong>This is the only time they're shown.</strong> Download the list and pass them on securely, then delete the file.</p>
            <div className="formactions"><button className="primary" onClick={() => download("dapper-new-people.csv",
              "username,name,temporary password\n" + withPw.map((r) => [r.username, r.name, r.temporaryPassword!].map(csvCell).join(",")).join("\n") + "\n")}>Download the list (with passwords)</button></div>
          </div>
        )}
        <table className="users">
          <thead><tr><th>Row</th><th>Username</th><th>Name</th><th>Result</th></tr></thead>
          <tbody>{done.rows.map((r) => (
            <tr key={r.row} className={r.status === "error" ? "bad" : ""}><td>{r.row}</td><td>{r.username}</td><td>{r.name}</td>
              <td>{r.status === "created" ? "Added" : r.message}</td></tr>
          ))}</tbody>
        </table>
        <div className="formactions"><a className="btn primary" href="#/people">Back to people</a><button onClick={() => { setDone(null); setCsv(""); }}>Import another file</button></div>
      </div>
    );
  }

  return (
    <div className="page narrow">
      <p><a href="#/people">← Back to people</a></p>
      <h2>Import people from a file</h2>
      <p className="sub">Use a CSV file (you can export one from a spreadsheet) with a header row. Needed columns: <strong>first name</strong> and <strong>last name</strong>. Optional: email, phone, mobile, title, department, username (otherwise first.last is used), password (otherwise a temporary one is made). Up to 500 people at a time.</p>
      <div className="panel">
        <label>Choose a file
          <input type="file" accept=".csv,text/csv" onChange={async (e) => { const f = e.target.files?.[0]; if (f) { setCsv(await f.text()); setCheck(null); } }} />
        </label>
        <label>…or paste it here
          <textarea rows={6} value={csv} onChange={(e) => { setCsv(e.target.value); setCheck(null); }} spellCheck={false} placeholder={TEMPLATE} />
        </label>
        <button type="button" className="link" onClick={() => download("people-template.csv", TEMPLATE)}>Download an example file</button>
        {groups.length > 0 && (
          <fieldset><legend>Add everyone to these groups (optional)</legend>
            {groups.map((g) => (
              <label className="inline" key={g.dn}>
                <input type="checkbox" checked={chosen.includes(g.dn)} onChange={(e) => setChosen(e.target.checked ? [...chosen, g.dn] : chosen.filter((x) => x !== g.dn))} /> {g.name}
                {g.admin && <span className="sub"> — administrators: can manage everyone</span>}
              </label>
            ))}
          </fieldset>
        )}
        {err && <Notice kind="error">{err}</Notice>}
        <div className="formactions"><button className="primary" disabled={!csv.trim() || busy} onClick={() => run(true)}>{busy && !check ? "Checking…" : "Check the file"}</button></div>
      </div>

      {check && (
        <div className="panel">
          <h3>What would happen</h3>
          <p>{check.ready} of {check.total} {check.total === 1 ? "row is" : "rows are"} ready to import{check.failed ? `; ${check.failed} need fixing first` : ""}. Nothing has been added yet.</p>
          <table className="users">
            <thead><tr><th>Row</th><th>Username</th><th>Name</th><th>Check</th></tr></thead>
            <tbody>{check.rows.map((r) => (
              <tr key={r.row} className={r.status === "error" ? "bad" : ""}><td>{r.row}</td><td>{r.username}</td><td>{r.name}</td>
                <td>{r.status === "ready" ? "Ready" : r.message}</td></tr>
            ))}</tbody>
          </table>
          {check.ready > 0 && (
            <>
              {check.failed > 0 && <p className="sub">The rows with problems will be skipped. Fix them in your file and import them separately if you like.</p>}
              <label className="inline"><input type="checkbox" checked={approved} onChange={(e) => setApproved(e.target.checked)} /> Yes, add these {check.ready} {check.ready === 1 ? "person" : "people"}</label>
              <div className="formactions"><button className="primary" disabled={!approved || busy} onClick={() => run(false)}>{busy ? "Importing…" : `Import ${check.ready} ${check.ready === 1 ? "person" : "people"}`}</button></div>
            </>
          )}
        </div>
      )}
    </div>
  );
}
