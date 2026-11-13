import { useEffect, useState } from "react";
import { api, UserPage } from "./api";
import { BulkAction, BulkDialog } from "./BulkActions";
import { href } from "./router";

type SortKey = "name" | "username" | "email";
const PAGE_SIZES = [10, 25, 50, 100];

const initials = (name: string) => name.split(/\s+/).filter(Boolean).slice(0, 2).map((w) => w[0]!.toUpperCase()).join("") || "?";

export function UserList() {
  const [input, setInput] = useState("");
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [sort, setSort] = useState<SortKey>("name");
  const [dir, setDir] = useState<"asc" | "desc">("asc");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  const [data, setData] = useState<UserPage | null>(null);
  const [err, setErr] = useState("");
  const [loading, setLoading] = useState(true);
  const [picked, setPicked] = useState<string[]>([]);
  const [bulk, setBulk] = useState<BulkAction | null>(null);
  const [reload, setReload] = useState(0);

  // Wait for a pause in typing before searching.
  useEffect(() => {
    const t = setTimeout(() => { setQ(input.trim()); setPage(1); }, 250);
    return () => clearTimeout(t);
  }, [input]);

  useEffect(() => {
    let current = true;
    setLoading(true);
    api.users({ q, sort, dir, status, page, pageSize })
      .then((d) => { if (current) { setData(d); setErr(""); } })
      .catch((x) => { if (current) setErr((x as Error).message); })
      .finally(() => { if (current) setLoading(false); });
    return () => { current = false; };
  }, [q, sort, dir, status, page, pageSize, reload]);

  const sortBy = (k: SortKey) => {
    if (k === sort) setDir(dir === "asc" ? "desc" : "asc"); else { setSort(k); setDir("asc"); }
    setPage(1);
  };
  const arrow = (k: SortKey) => (k === sort ? (dir === "asc" ? " ▲" : " ▼") : "");
  const ariaSort = (k: SortKey) => (k === sort ? (dir === "asc" ? "ascending" : "descending") : "none");

  const total = data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / pageSize));
  const from = total === 0 ? 0 : (page - 1) * pageSize + 1;
  const to = Math.min(total, page * pageSize);

  return (
    <div className="page">
      <div className="pagehead">
        <h2>People</h2>
        <div className="actions">
          <a className="btn" href={`/api/admin/users?format=csv&q=${encodeURIComponent(q)}&status=${status}&sort=${sort}&dir=${dir}`}>Export CSV</a>
          <a className="btn" href="#/people/import">Import…</a>
          <a className="btn" href="#/units">Units…</a>
          <a className="btn primary" href="#/people/new">+ New person</a>
        </div>
      </div>
      <div className="list-tools">
        <input type="search" className="search" aria-label="Search people"
          placeholder="Search by name, username, email or phone"
          value={input} onChange={(e) => setInput(e.target.value)} autoFocus />
        <label className="inline">Show
          <select value={status} onChange={(e) => { setStatus(e.target.value); setPage(1); }}>
            <option value="">Everyone</option>
            <option value="active">Active only</option>
            <option value="suspended">Suspended only</option>
          </select>
        </label>
        <label className="inline">Per page
          <select value={pageSize} onChange={(e) => { setPageSize(Number(e.target.value)); setPage(1); }}>
            {PAGE_SIZES.map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
        </label>
      </div>

      {err && <p className="error" role="alert">{err}</p>}

      {picked.length > 0 && (
        <div className="bulkbar" role="region" aria-label="Actions for the selected people">
          <strong>{picked.length} selected</strong>
          <button onClick={() => setBulk("suspend")}>Suspend</button>
          <button onClick={() => setBulk("restore")}>Restore</button>
          <button onClick={() => setBulk("addGroup")}>Add to group…</button>
          <button onClick={() => setBulk("removeGroup")}>Remove from group…</button>
          <button className="danger" onClick={() => setBulk("delete")}>Delete…</button>
          <span className="spacer" />
          <button className="plain" onClick={() => setPicked([])}>Clear selection</button>
        </div>
      )}

      <table className="users" aria-busy={loading}>
        <thead>
          <tr>
            <th className="check"><input type="checkbox" aria-label="Select everyone on this page"
              checked={!!data && data.users.length > 0 && data.users.every((u) => picked.includes(u.dn))}
              onChange={(e) => {
                const here = data?.users.map((u) => u.dn) ?? [];
                setPicked(e.target.checked ? Array.from(new Set([...picked, ...here])) : picked.filter((d) => !here.includes(d)));
              }} /></th>
            <th aria-sort={ariaSort("name")}><button className="th" onClick={() => sortBy("name")}>Name{arrow("name")}</button></th>
            <th aria-sort={ariaSort("username")}><button className="th" onClick={() => sortBy("username")}>Username{arrow("username")}</button></th>
            <th aria-sort={ariaSort("email")}><button className="th" onClick={() => sortBy("email")}>Email{arrow("email")}</button></th>
            <th>Phone</th>
            <th>Status</th>
          </tr>
        </thead>
        <tbody>
          {data?.users.map((u) => (
            <tr key={u.dn} className={u.suspended ? "dim" : ""}>
              <td className="check"><input type="checkbox" aria-label={`Select ${u.name}`} checked={picked.includes(u.dn)}
                onChange={(e) => setPicked(e.target.checked ? [...picked, u.dn] : picked.filter((d) => d !== u.dn))} /></td>
              <td>
                <div className="namecell">
                  {u.hasPhoto
                    ? <img className="avatar small" src={api.thumbUrl(u.dn)} alt="" width={36} height={36} loading="lazy" decoding="async" />
                    : <span className="avatar small blank" aria-hidden="true">{initials(u.name)}</span>}
                  <span><a className="rowlink" href={href("#/person", u.dn)}>{u.name}</a>{u.title && <div className="sub">{u.title}</div>}</span>
                </div>
              </td>
              <td>{u.username}</td>
              <td>{u.email}</td>
              <td>{(u.phones ?? []).map((p) => <div key={p}>{p}</div>)}</td>
              <td>{u.suspended ? <span className="badge off">Suspended</span> : <span className="badge on">Active</span>}</td>
            </tr>
          ))}
          {data && data.users.length === 0 && (
            <tr><td colSpan={6} className="empty">{q ? `No one matches “${q}”.` : "No people found."}</td></tr>
          )}
        </tbody>
      </table>

      <div className="pager">
        <span aria-live="polite">{total === 0 ? "0 people" : `Showing ${from}–${to} of ${total}`}</span>
        <span className="spacer" />
        <button onClick={() => setPage(1)} disabled={page <= 1}>« First</button>
        <button onClick={() => setPage(page - 1)} disabled={page <= 1}>‹ Previous</button>
        <span>Page {page} of {pages}</span>
        <button onClick={() => setPage(page + 1)} disabled={page >= pages}>Next ›</button>
        <button onClick={() => setPage(pages)} disabled={page >= pages}>Last »</button>
      </div>
      {bulk && <BulkDialog action={bulk} dns={picked} onClose={() => setBulk(null)} onDone={() => { setPicked([]); setReload((n) => n + 1); }} />}
      {data?.truncated && <p className="sub">Very large directory: only the first 5,000 accounts are searchable.</p>}
    </div>
  );
}
