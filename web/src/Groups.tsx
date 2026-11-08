import { useEffect, useRef, useState } from "react";
import { api, GroupDetail as Group, GroupKind, GroupPage, User } from "./api";
import { go, href } from "./router";
import { Modal, Notice } from "./ui";
import { ChangeGroupID, GroupChecker } from "./GroupIDs";

export const KIND_LABEL: Record<GroupKind, string> = { shared: "Group", linux: "Linux group", personal: "Personal group" };
const KIND_HELP: Record<GroupKind, string> = {
  shared: "Members are listed by full name. Used for permissions and by applications.",
  linux: "A Linux group with a group ID. Members are listed by username.",
  personal: "A group named after one person, which is their primary group on Linux.",
};

export function Groups() {
  const [input, setInput] = useState("");
  const [q, setQ] = useState("");
  const [kind, setKind] = useState("");
  const [page, setPage] = useState(1);
  const [data, setData] = useState<GroupPage | null>(null);
  const [name, setName] = useState("");
  const [newKind, setNewKind] = useState<"standard" | "linux">("standard");
  const [err, setErr] = useState("");

  useEffect(() => {
    const t = setTimeout(() => { setQ(input.trim()); setPage(1); }, 250);
    return () => clearTimeout(t);
  }, [input]);
  useEffect(() => {
    let current = true;
    api.groups({ kind, q, page, pageSize: 25 })
      .then((d) => { if (current) { setData(d); setErr(""); } })
      .catch((x) => { if (current) setErr((x as Error).message); });
    return () => { current = false; };
  }, [kind, q, page]);

  const c = data?.counts ?? {};
  const total = data?.total ?? 0;
  const pages = Math.max(1, Math.ceil(total / (data?.pageSize ?? 25)));
  const tab = (value: string, label: string, count?: number) => (
    <button type="button" className={kind === value ? "tab on" : "tab"} aria-pressed={kind === value}
      onClick={() => { setKind(value); setPage(1); }}>{label}{count !== undefined && ` (${count})`}</button>
  );

  return (
    <div className="page narrow">
      <h2>Groups</h2>
      <p className="sub">Groups collect people so applications, servers and permissions can refer to them together.</p>

      <form className="list-tools" onSubmit={async (e) => {
        e.preventDefault(); setErr("");
        try { const r = await api.createGroup(name.trim(), newKind); setName(""); go(href("/group", r.dn)); }
        catch (x) { setErr((x as Error).message); }
      }}>
        <input aria-label="New group name" placeholder="New group name" value={name} onChange={(e) => setName(e.target.value)} />
        <select aria-label="Kind of group" value={newKind} onChange={(e) => setNewKind(e.target.value as "standard" | "linux")}>
          <option value="standard">Standard group</option>
          <option value="linux">Linux group (gets a group ID)</option>
        </select>
        <button className="primary" disabled={!name.trim()}>Create group</button>
      </form>
      {err && <Notice kind="error">{err}</Notice>}

      <GroupChecker />

      <div className="list-tools">
        <nav className="subtabs" aria-label="Kinds of group">
          {tab("", "All", (c.shared ?? 0) + (c.linux ?? 0) + (c.personal ?? 0))}
          {tab("shared", "Groups", c.shared ?? 0)}
          {tab("linux", "Linux groups", c.linux ?? 0)}
          {tab("personal", "Personal groups", c.personal ?? 0)}
        </nav>
        <input type="search" className="search" aria-label="Search groups" placeholder="Search by name or group ID" value={input} onChange={(e) => setInput(e.target.value)} />
      </div>
      {kind === "personal" && <p className="sub">{KIND_HELP.personal}</p>}

      <table className="users">
        <thead><tr><th>Group</th><th>Type</th><th>Group ID</th><th>Members</th></tr></thead>
        <tbody>
          {data?.groups.map((g) => (
            <tr key={g.dn} className={g.kind === "personal" ? "dim" : ""}>
              <td><a className="rowlink" href={href("#/group", g.dn)}>{g.name}</a>{g.admin && <span className="badge on"> administrators</span>}</td>
              <td title={KIND_HELP[g.kind]}>{KIND_LABEL[g.kind]}</td>
              <td>{g.gid ?? ""}</td>
              <td>{g.kind === "personal" ? "—" : g.members}</td>
            </tr>
          ))}
          {data && data.groups.length === 0 && <tr><td colSpan={4} className="empty">{q ? `No group matches “${q}”.` : "No groups yet."}</td></tr>}
        </tbody>
      </table>

      <div className="pager">
        <span aria-live="polite">{total === 0 ? "0 groups" : `Page ${page} of ${pages} · ${total} group${total === 1 ? "" : "s"}`}</span>
        <span className="spacer" />
        <button onClick={() => setPage(page - 1)} disabled={page <= 1}>‹ Previous</button>
        <button onClick={() => setPage(page + 1)} disabled={page >= pages}>Next ›</button>
      </div>
    </div>
  );
}

export function GroupDetail({ dn }: { dn: string }) {
  const [g, setG] = useState<Group | null>(null);
  const [err, setErr] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [changeID, setChangeID] = useState(false);
  const [done, setDone] = useState("");
  const load = () => api.group(dn).then((d) => { setG(d); setErr(""); }).catch((x) => setErr((x as Error).message));
  useEffect(() => { setG(null); load(); }, [dn]);

  if (!g) return <div className="page">{err ? <Notice kind="error">{err}</Notice> : "Loading…"}<p><a href="#/groups">← Back to groups</a></p></div>;

  return (
    <div className="page narrow">
      <p><a href="#/groups">← Back to groups</a></p>
      <div className="pagehead">
        <div>
          <h2>{g.name}</h2>
          <div className="sub">{KIND_LABEL[g.kind]}{g.gid ? ` · group ID ${g.gid}` : ""}</div>
        </div>
        <div className="actions">
          {g.gid && <button onClick={() => setChangeID(true)}>Change group ID…</button>}
          <button className="danger" onClick={() => setConfirm(true)}>Delete group…</button>
        </div>
      </div>
      <p className="sub">{KIND_HELP[g.kind]}</p>
      {done && <Notice kind="ok">{done}</Notice>}
      {err && <Notice kind="error">{err}</Notice>}

      <div className="panel">
        <h3>Add someone</h3>
        <MemberPicker existing={[...g.members, ...g.primaryMembers].map((m) => m.dn.toLowerCase())} onPick={async (u) => {
          try { await api.addMember(dn, u.dn); await load(); } catch (x) { setErr((x as Error).message); }
        }} />
      </div>

      <h3>Members</h3>
      <table className="users">
        <thead><tr><th>Member</th><th>Username</th><th /></tr></thead>
        <tbody>
          {g.members.map((m) => (
            <tr key={m.dn || m.username}>
              <td>{m.dn ? <a className="rowlink" href={href("#/person", m.dn)}>{m.name}</a> : <span title="No account with this username was found">{m.name}</span>}</td>
              <td>{m.username}</td>
              <td className="right">{m.dn && <button onClick={async () => {
                try { await api.removeMember(dn, m.dn); await load(); } catch (x) { setErr((x as Error).message); }
              }}>Remove</button>}</td>
            </tr>
          ))}
          {g.members.length === 0 && <tr><td colSpan={3} className="empty">No members listed.</td></tr>}
        </tbody>
      </table>

      {g.primaryMembers.length > 0 && (
        <>
          <h3>People whose primary group this is</h3>
          <p className="sub">These people belong to it because their account's group ID points here. To change that, edit the person's primary group.</p>
          <table className="users">
            <tbody>
              {g.primaryMembers.map((m) => (
                <tr key={m.dn}><td><a className="rowlink" href={href("#/person", m.dn)}>{m.name}</a></td><td>{m.username}</td></tr>
              ))}
            </tbody>
          </table>
        </>
      )}

      {changeID && g.gid && (
        <ChangeGroupID dn={dn} name={g.name} current={g.gid} onClose={() => setChangeID(false)}
          onDone={(m) => { setChangeID(false); setDone(m); load(); }} />
      )}

      {confirm && (
        <Modal title={`Delete ${g.name}?`} onClose={() => setConfirm(false)}>
          {g.protected ? <Notice kind="error">{g.protected}</Notice> : (
            <p>The group is removed. The people in it are not affected, but anything that relied on this group will lose it.</p>
          )}
          <div className="formactions">
            <button className="danger" disabled={!!g.protected} onClick={async () => {
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
