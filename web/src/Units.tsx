import { useCallback, useEffect, useState } from "react";
import { api, Unit } from "./api";
import { Modal, Notice } from "./ui";

export const unitLabel = (u: Unit) => `${"\u2003".repeat(u.depth)}${u.root ? "Top level (no unit)" : u.name}`;

export function Units() {
  const [units, setUnits] = useState<Unit[] | null>(null);
  const [err, setErr] = useState("");
  const [ok, setOk] = useState("");
  const [name, setName] = useState("");
  const [parent, setParent] = useState("");
  const [rename, setRename] = useState<Unit | null>(null);
  const [remove, setRemove] = useState<Unit | null>(null);

  const load = useCallback(() => api.units().then((r) => {
    setUnits(r.units); setParent((p) => p || r.baseDN);
  }).catch((x) => setErr((x as Error).message)), []);
  useEffect(() => { load(); }, [load]);

  return (
    <div className="page narrow">
      <p><a href="#/people">← Back to people</a></p>
      <h2>Units</h2>
      <p className="sub">Units are sections of your people list, such as a department or a site. A person belongs to one unit. A unit can be renamed or removed only while it is empty.</p>

      <form className="list-tools" onSubmit={async (e) => {
        e.preventDefault(); setErr(""); setOk("");
        try { await api.createUnit(parent, name.trim()); setOk(`Added "${name.trim()}".`); setName(""); await load(); }
        catch (x) { setErr((x as Error).message); }
      }}>
        <input aria-label="New unit name" placeholder="New unit name" value={name} onChange={(e) => setName(e.target.value)} />
        <select aria-label="Inside" value={parent} onChange={(e) => setParent(e.target.value)}>
          {(units ?? []).map((u) => <option key={u.dn} value={u.dn}>{unitLabel(u)}</option>)}
        </select>
        <button className="primary" disabled={!name.trim()}>Add unit</button>
      </form>
      {err && <Notice kind="error">{err}</Notice>}
      {ok && <Notice kind="ok">{ok}</Notice>}

      {!units ? "Loading\u2026" : (
        <table className="table">
          <thead><tr><th scope="col">Unit</th><th scope="col">People</th><th scope="col">Units inside</th><th scope="col"><span className="sr-only">Actions</span></th></tr></thead>
          <tbody>
            {units.map((u) => (
              <tr key={u.dn}>
                <td style={{ paddingLeft: `${0.75 + u.depth * 1.25}rem` }}>{u.root ? <em>Top level (no unit)</em> : u.name}</td>
                <td>{u.people}</td>
                <td>{u.children}</td>
                <td>
                  {!u.root && (
                    <>
                      <button disabled={u.people + u.children > 0} title={u.people + u.children > 0 ? "Only empty units can be renamed" : ""} onClick={() => setRename(u)}>Rename</button>{" "}
                      <button className="danger" disabled={u.people + u.children > 0} title={u.people + u.children > 0 ? "Only empty units can be removed" : ""} onClick={() => setRemove(u)}>Remove</button>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {rename && <RenameDialog unit={rename} onClose={() => setRename(null)} onDone={() => { setRename(null); load(); }} />}
      {remove && (
        <Modal title={`Remove ${remove.name}?`} onClose={() => setRemove(null)}>
          <p>This removes the empty unit. No people are affected.</p>
          <div className="formactions">
            <button className="danger" onClick={async () => {
              try { await api.deleteUnit(remove.dn); setRemove(null); await load(); } catch (x) { setErr((x as Error).message); setRemove(null); }
            }}>Remove unit</button>
            <button onClick={() => setRemove(null)}>Cancel</button>
          </div>
        </Modal>
      )}
    </div>
  );
}

function RenameDialog({ unit, onClose, onDone }: { unit: Unit; onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState(unit.name);
  const [err, setErr] = useState("");
  return (
    <Modal title={`Rename ${unit.name}`} onClose={onClose}>
      <form onSubmit={async (e) => {
        e.preventDefault();
        try { await api.renameUnit(unit.dn, name.trim()); onDone(); } catch (x) { setErr((x as Error).message); }
      }}>
        <label>New name<input value={name} onChange={(e) => setName(e.target.value)} autoFocus /></label>
        {err && <Notice kind="error">{err}</Notice>}
        <div className="formactions">
          <button className="primary" disabled={!name.trim() || name.trim() === unit.name}>Rename</button>
          <button type="button" onClick={onClose}>Cancel</button>
        </div>
      </form>
    </Modal>
  );
}
