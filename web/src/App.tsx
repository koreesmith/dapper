import { useEffect, useState } from "react";
import { api, ConnectConfig, Entry } from "./api";

export function App() {
  const [baseDN, setBaseDN] = useState<string | null>(null);
  const [checked, setChecked] = useState(false);

  useEffect(() => {
    api.session().then((s) => setBaseDN(s.baseDN)).catch(() => {}).finally(() => setChecked(true));
  }, []);

  if (!checked) return null;
  if (!baseDN) return <ConnectForm onConnected={setBaseDN} />;
  return <Browser baseDN={baseDN} onDisconnect={() => api.disconnect().then(() => setBaseDN(null))} />;
}

function ConnectForm({ onConnected }: { onConnected: (b: string) => void }) {
  const [cfg, setCfg] = useState<ConnectConfig>({
    url: "ldap://localhost:389", bindDN: "cn=admin,dc=example,dc=org", password: "",
    baseDN: "dc=example,dc=org", startTLS: false, skipVerify: false,
  });
  const [err, setErr] = useState("");
  const set = (k: keyof ConnectConfig, v: string | boolean) => setCfg({ ...cfg, [k]: v });

  return (
    <form className="card" onSubmit={async (e) => {
      e.preventDefault();
      try { onConnected((await api.connect(cfg)).baseDN); } catch (x) { setErr(String(x)); }
    }}>
      <h1>Dapper</h1>
      <label>Server URL<input value={cfg.url} onChange={(e) => set("url", e.target.value)} /></label>
      <label>Bind DN<input value={cfg.bindDN} onChange={(e) => set("bindDN", e.target.value)} /></label>
      <label>Password<input type="password" value={cfg.password} onChange={(e) => set("password", e.target.value)} /></label>
      <label>Base DN<input value={cfg.baseDN} onChange={(e) => set("baseDN", e.target.value)} /></label>
      <label className="inline"><input type="checkbox" checked={cfg.startTLS} onChange={(e) => set("startTLS", e.target.checked)} /> StartTLS</label>
      <label className="inline"><input type="checkbox" checked={cfg.skipVerify} onChange={(e) => set("skipVerify", e.target.checked)} /> Skip certificate verification</label>
      {err && <p className="error">{err}</p>}
      <button>Connect</button>
    </form>
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

function Browser({ baseDN, onDisconnect }: { baseDN: string; onDisconnect: () => void }) {
  const [selected, setSelected] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const [results, setResults] = useState<Entry[] | null>(null);
  const [err, setErr] = useState("");

  return (
    <div className="layout">
      <header>
        <strong>Dapper</strong>
        <form onSubmit={async (e) => {
          e.preventDefault();
          try { setResults(filter ? await api.search(baseDN, filter) : null); setErr(""); } catch (x) { setErr(String(x)); }
        }}>
          <input placeholder="LDAP filter, e.g. (uid=jdoe)" value={filter} onChange={(e) => setFilter(e.target.value)} />
        </form>
        <button onClick={onDisconnect}>Disconnect</button>
      </header>
      <aside>
        {results ? (
          <ul>{results.map((r) => <li key={r.dn}><span className="node" onClick={() => setSelected(r.dn)}>{r.dn}</span></li>)}</ul>
        ) : (
          <ul><TreeNode entry={{ dn: baseDN, attrs: {} }} onSelect={setSelected} selected={selected} /></ul>
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
