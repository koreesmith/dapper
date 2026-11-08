import { useEffect, useState } from "react";
import { api, Entry, Me } from "./api";

export function App() {
  const [me, setMe] = useState<Me | null>(null);
  const [checked, setChecked] = useState(false);

  useEffect(() => {
    api.me().then(setMe).catch(() => {}).finally(() => setChecked(true));
  }, []);

  if (!checked) return null;
  if (!me) return <Login onSignedIn={setMe} />;
  return <Shell me={me} onSignOut={() => api.logout().then(() => setMe(null))} />;
}

function Login({ onSignedIn }: { onSignedIn: (m: Me) => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  return (
    <form className="card" onSubmit={async (e) => {
      e.preventDefault();
      setBusy(true); setErr("");
      try { onSignedIn(await api.login(username, password)); }
      catch (x) { setErr((x as Error).message); }
      finally { setBusy(false); }
    }}>
      <h1>Dapper</h1>
      <label>Username or email
        <input autoFocus autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} />
      </label>
      <label>Password
        <input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
      </label>
      {err && <p className="error" role="alert">{err}</p>}
      <button disabled={busy || !username || !password}>{busy ? "Signing in…" : "Sign in"}</button>
    </form>
  );
}

function Shell({ me, onSignOut }: { me: Me; onSignOut: () => void }) {
  const [tab, setTab] = useState<"me" | "directory">("me");
  return (
    <div className="shell">
      <header>
        <strong>Dapper</strong>
        <nav>
          <button className={tab === "me" ? "tab on" : "tab"} onClick={() => setTab("me")}>My account</button>
          {me.isAdmin && (
            <button className={tab === "directory" ? "tab on" : "tab"} onClick={() => setTab("directory")}>Directory</button>
          )}
        </nav>
        <span className="spacer" />
        <span>{me.name}{me.isAdmin ? " (admin)" : ""}</span>
        <button onClick={onSignOut}>Sign out</button>
      </header>
      {tab === "me" || !me.isAdmin ? (
        <main className="page"><h2>Welcome, {me.name}</h2><p>Profile and password management are coming next.</p></main>
      ) : (
        <Browser />
      )}
    </div>
  );
}

const rdn = (dn: string) => dn.split(",")[0];

function TreeNode({ entry, onSelect, selected }: { entry: Entry; onSelect: (dn: string) => void; selected: string | null }) {
  const [open, setOpen] = useState(false);
  const [kids, setKids] = useState<Entry[] | null>(null);
  const toggle = async () => {
    if (!open && !kids) setKids(await api.children(entry.dn));
    setOpen(!open);
  };
  return (
    <li>
      <span className="twist" onClick={toggle}>{open ? "▾" : "▸"}</span>
      <span className={entry.dn === selected ? "node sel" : "node"} onClick={() => onSelect(entry.dn)}>{rdn(entry.dn)}</span>
      {open && kids && <ul>{kids.map((k) => <TreeNode key={k.dn} entry={k} onSelect={onSelect} selected={selected} />)}</ul>}
    </li>
  );
}

function Browser() {
  const [selected, setSelected] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const [results, setResults] = useState<Entry[] | null>(null);
  const [root, setRoot] = useState<Entry[] | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => { api.children().then(setRoot).catch((x) => setErr(String(x))); }, []);

  return (
    <div className="layout">
      <div className="toolbar">
        <form onSubmit={async (e) => {
          e.preventDefault();
          try { setResults(filter ? await api.search("", filter) : null); setErr(""); } catch (x) { setErr(String(x)); }
        }}>
          <input placeholder="LDAP filter, e.g. (uid=jdoe)" value={filter} onChange={(e) => setFilter(e.target.value)} />
        </form>
      </div>
      <aside>
        {results ? (
          <ul>{results.map((r) => <li key={r.dn}><span className="node" onClick={() => setSelected(r.dn)}>{r.dn}</span></li>)}</ul>
        ) : (
          <ul>{root?.map((r) => <TreeNode key={r.dn} entry={r} onSelect={setSelected} selected={selected} />)}</ul>
        )}
        {err && <p className="error">{err}</p>}
      </aside>
      <main>{selected ? <EntryEditor key={selected} dn={selected} /> : <p>Select an entry.</p>}</main>
    </div>
  );
}

function EntryEditor({ dn }: { dn: string }) {
  const [entry, setEntry] = useState<Entry | null>(null);
  const [draft, setDraft] = useState<Record<string, string[]>>({});
  const [msg, setMsg] = useState("");
  const load = () => api.get(dn).then((e) => { setEntry(e); setDraft(e.attrs); }).catch((x) => setMsg(String(x)));
  useEffect(() => { load(); }, [dn]);
  if (!entry) return <p>{msg || "Loading…"}</p>;

  const save = async () => {
    const changes = Object.keys(draft)
      .filter((k) => JSON.stringify(draft[k]) !== JSON.stringify(entry.attrs[k]))
      .map((k) => ({ op: "replace" as const, attr: k, values: draft[k].filter((v) => v !== "") }));
    if (!changes.length) return setMsg("No changes.");
    try { await api.modify(dn, changes); setMsg("Saved."); load(); } catch (x) { setMsg(String(x)); }
  };
  const del = async () => {
    if (!confirm(`Delete ${dn}?`)) return;
    try { await api.remove(dn); setMsg("Deleted. Reload the tree to refresh."); setEntry(null); } catch (x) { setMsg(String(x)); }
  };

  return (
    <div>
      <h2>{dn}</h2>
      <table>
        <tbody>
          {Object.entries(draft).map(([k, vals]) => (
            <tr key={k}>
              <th>{k}</th>
              <td>{vals.map((v, i) => (
                <input key={i} value={v} onChange={(e) => setDraft({ ...draft, [k]: vals.map((x, j) => (j === i ? e.target.value : x)) })} />
              ))}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <button onClick={save}>Save</button> <button className="danger" onClick={del}>Delete</button>
      {msg && <p>{msg}</p>}
    </div>
  );
}
