import { useEffect, useRef, useState } from "react";
import { api, GroupDetail as Group, GroupSummary, User } from "./api";
import { go, href } from "./router";
import { Modal, Notice } from "./ui";

export function Groups() {
  const [groups, setGroups] = useState<GroupSummary[] | null>(null);
  const [name, setName] = useState("");
  const [err, setErr] = useState("");
  const load = () => api.groups().then(setGroups).catch((x) => setErr((x as Error).message));
  useEffect(() => { load(); }, []);

  return (
    <div className="page narrow">
      <h2>Groups</h2>
      <p className="sub">Groups collect people so applications and permissions can refer to them together.</p>
      <form className="list-tools" onSubmit={async (e) => {
        e.preventDefault(); setErr("");
        try { const r = await api.createGroup(name.trim()); setName(""); go(href("/group", r.dn)); }
        catch (x) { setErr((x as Error).message); }
      }}>
        <input aria-label="New group name" placeholder="New group name" value={name} onChange={(e) => setName(e.target.value)} />
        <button className="primary" disabled={!name.trim()}>Create group</button>
      </form>
      {err && <Notice kind="error">{err}</Notice>}
      <table className="users">
        <thead><tr><th>Group</th><th>Members</th></tr></thead>
        <tbody>
          {groups?.map((g) => (
            <tr key={g.dn}><td><a className="rowlink" href={href("#/group", g.dn)}>{g.name}</a></td><td>{g.members}</td></tr>
          ))}
          {groups && groups.length === 0 && <tr><td colSpan={2} className="empty">No groups yet.</td></tr>}
        </tbody>
      </table>
    </div>
  );
}

export function GroupDetail({ dn }: { dn: string }) {
  const [g, setG] = useState<Group | null>(null);
  const [err, setErr] = useState("");
  const [confirm, setConfirm] = useState(false);
  const load = () => api.group(dn).then((d) => { setG(d); setErr(""); }).catch((x) => setErr((x as Error).message));
  useEffect(() => { setG(null); load(); }, [dn]);

  if (!g) return <div className="page">{err ? <Notice kind="error">{err}</Notice> : "Loading…"}<p><a href="#/groups">← Back to groups</a></p></div>;

  return (
    <div className="page narrow">
      <p><a href="#/groups">← Back to groups</a></p>
      <div className="pagehead">
        <h2>{g.name}</h2>
        <button className="danger" onClick={() => setConfirm(true)}>Delete group…</button>
      </div>
      {err && <Notice kind="error">{err}</Notice>}

      <div className="panel">
        <h3>Add someone</h3>
        <MemberPicker existing={g.members.map((m) => m.dn.toLowerCase())} onPick={async (u) => {
          try { await api.addMember(dn, u.dn); await load(); } catch (x) { setErr((x as Error).message); }
        }} />
      </div>

      <table className="users">
        <thead><tr><th>Member</th><th>Username</th><th /></tr></thead>
        <tbody>
          {g.members.map((m) => (
            <tr key={m.dn}>
              <td><a className="rowlink" href={href("#/person", m.dn)}>{m.name}</a></td>
              <td>{m.username}</td>
              <td className="right"><button onClick={async () => {
                try { await api.removeMember(dn, m.dn); await load(); } catch (x) { setErr((x as Error).message); }
              }}>Remove</button></td>
            </tr>
          ))}
          {g.members.length === 0 && <tr><td colSpan={3} className="empty">No members yet.</td></tr>}
        </tbody>
      </table>

      {confirm && (
        <Modal title={`Delete ${g.name}?`} onClose={() => setConfirm(false)}>
          <p>The group is removed. The people in it are not affected, but anything that relied on this group will lose it.</p>
          <div className="formactions">
            <button className="danger" onClick={async () => {
              try { await api.deleteGroup(dn); go("/groups"); } catch (x) { setErr((x as Error).message); setConfirm(false); }
            }}>Delete group</button>
            <button onClick={() => setConfirm(false)}>Cancel</button>
          </div>
        </Modal>
      )}
    </div>
  );
}

function MemberPicker({ existing, onPick }: { existing: string[]; onPick: (u: User) => void }) {
  const [q, setQ] = useState("");
  const [results, setResults] = useState<User[]>([]);
  const seq = useRef(0);
  useEffect(() => {
    if (q.trim().length < 2) { setResults([]); return; }
    const mine = ++seq.current;
    const t = setTimeout(() => {
      api.users({ q: q.trim(), sort: "name", dir: "asc", status: "active", page: 1, pageSize: 8 })
        .then((d) => { if (mine === seq.current) setResults(d.users.filter((u) => !existing.includes(u.dn.toLowerCase()))); })
        .catch(() => {});
    }, 200);
    return () => clearTimeout(t);
  }, [q, existing]);

  return (
    <div>
      <input type="search" aria-label="Find a person to add" placeholder="Search by name, username or email"
        value={q} onChange={(e) => setQ(e.target.value)} />
      {results.length > 0 && (
        <ul className="picker">
          {results.map((u) => (
            <li key={u.dn}>
              <button type="button" onClick={() => { onPick(u); setQ(""); setResults([]); }}>
                {u.name} <span className="sub">{u.username}{u.email ? ` · ${u.email}` : ""}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
