import { useEffect, useState } from "react";
import { api, ActivityPage } from "./api";
import { Notice } from "./ui";

const LABELS: Record<string, string> = {
  login: "Signed in", "login.failed": "Failed sign-in", "login.blocked": "Sign-in blocked (too many tries)",
  "user.create": "Created an account", "user.update": "Edited an account", "user.suspend": "Suspended an account",
  "user.restore": "Restored an account", "user.delete": "Deleted an account", "user.password.reset": "Reset a password",
  "user.gid.change": "Changed a primary group", "user.import": "Imported accounts", "user.bulk": "Bulk change to accounts",
  "password.change": "Changed their own password", "profile.update": "Edited their own details", "photo.update": "Changed a photo",
  "group.create": "Created a group", "group.delete": "Deleted a group", "group.member.add": "Added a group member",
  "group.member.remove": "Removed a group member", "group.gid.change": "Changed a group ID",
  "ou.create": "Created an OU", "ou.rename": "Renamed an OU", "ou.delete": "Deleted an OU", "user.move": "Moved an account",
  "settings.update": "Changed settings", "settings.fields.update": "Changed what people can edit", "settings.container.create": "Created a container",
  "setup.complete": "Finished setup", "overlays.unlock": "Unlocked server settings", "overlays.enable": "Turned on a server feature",
  "overlays.update": "Changed a server feature", "overlays.disable": "Turned off a server feature", "overlays.failed": "A server change failed",
  "overlays.memberof.refresh": "Refreshed memberOf", "raw.add": "Added an entry (Advanced)", "raw.modify": "Edited an entry (Advanced)",
  "raw.delete": "Deleted an entry (Advanced)", "server.schema.add": "Added a schema", "server.index": "Changed an index", "server.acl": "Changed access rules",
  "server.tls": "Changed TLS settings",
};
const CATEGORIES = [
  ["", "Everything"], ["login", "Sign-ins"], ["user", "People"], ["group", "Groups"], ["password", "Passwords"],
  ["ou", "OUs"], ["settings", "Settings"], ["overlays", "Server features"], ["server", "Server configuration"], ["raw", "Advanced edits"],
];

const shortName = (dn?: string) => {
  if (!dn || dn === "-") return "";
  const first = dn.split(",")[0];
  const i = first.indexOf("=");
  return i >= 0 ? first.slice(i + 1) : first;
};

export function Activity() {
  const [input, setInput] = useState("");
  const [q, setQ] = useState("");
  const [category, setCategory] = useState("");
  const [page, setPage] = useState(1);
  const [data, setData] = useState<ActivityPage | null>(null);
  const [err, setErr] = useState("");

  useEffect(() => {
    const t = setTimeout(() => { setQ(input.trim()); setPage(1); }, 250);
    return () => clearTimeout(t);
  }, [input]);
  useEffect(() => {
    let current = true;
    api.activity({ q, category, page }).then((d) => { if (current) { setData(d); setErr(""); } })
      .catch((x) => { if (current) setErr((x as Error).message); });
    return () => { current = false; };
  }, [q, category, page]);

  const total = data?.total ?? 0;
  const size = data?.pageSize ?? 50;
  const pages = Math.max(1, Math.ceil(total / size));
  const csv = `/api/admin/audit?format=csv&q=${encodeURIComponent(q)}&category=${category}`;

  return (
    <div className="page">
      <div className="pagehead">
        <div><h2>Activity</h2><p className="sub">Who did what, newest first. Passwords are never recorded.</p></div>
        <a className="btn" href={csv}>Download as CSV</a>
      </div>
      <div className="list-tools">
        <input type="search" className="search" aria-label="Search activity" placeholder="Search by person, action or account" value={input} onChange={(e) => setInput(e.target.value)} />
        <label className="inline">Show
          <select value={category} onChange={(e) => { setCategory(e.target.value); setPage(1); }}>
            {CATEGORIES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
          </select>
        </label>
      </div>
      {err && <Notice kind="error">{err}</Notice>}
      <table className="users" tabIndex={0} aria-label="Activity">
        <thead><tr><th>When</th><th>Who</th><th>What</th><th>About</th><th>Details</th></tr></thead>
        <tbody>
          {data?.events.map((e, i) => (
            <tr key={i} className={e.action.startsWith("login.") || e.action === "overlays.failed" ? "bad" : ""}>
              <td><time dateTime={e.time}>{new Date(e.time).toLocaleString()}</time></td>
              <td>{shortName(e.actor) || <span className="sub">not signed in</span>}</td>
              <td>{LABELS[e.action] ?? e.action}</td>
              <td title={e.target}>{shortName(e.target) || e.target}</td>
              <td className="sub">{e.detail ? Object.entries(e.detail).map(([k, v]) => `${k}: ${shortName(v) || v}`).join(", ") : ""}</td>
            </tr>
          ))}
          {data && data.events.length === 0 && <tr><td colSpan={5} className="empty">Nothing recorded{q || category ? " for that search" : " yet"}.</td></tr>}
        </tbody>
      </table>
      <div className="pager">
        <span aria-live="polite">{total === 0 ? "0 events" : `Page ${page} of ${pages} · ${total} event${total === 1 ? "" : "s"}`}</span>
        <span className="spacer" />
        <button onClick={() => setPage(page - 1)} disabled={page <= 1}>‹ Previous</button>
        <button onClick={() => setPage(page + 1)} disabled={page >= pages}>Next ›</button>
      </div>
    </div>
  );
}
