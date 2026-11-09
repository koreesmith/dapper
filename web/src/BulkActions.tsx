import { useEffect, useState } from "react";
import { api, BulkResult, GroupSummary } from "./api";
import { Modal, Notice } from "./ui";

export type BulkAction = "suspend" | "restore" | "delete" | "addGroup" | "removeGroup";

const TITLES: Record<BulkAction, string> = {
  suspend: "Suspend", restore: "Restore", delete: "Delete", addGroup: "Add to a group", removeGroup: "Remove from a group",
};

const shortName = (dn: string) => { const f = dn.split(",")[0]; return f.slice(f.indexOf("=") + 1); };

/** Confirms and runs one action on everyone selected, then reports who it worked for. */
export function BulkDialog({ action, dns, onClose, onDone }: { action: BulkAction; dns: string[]; onClose: () => void; onDone: () => void }) {
  const [groups, setGroups] = useState<GroupSummary[]>([]);
  const [group, setGroup] = useState("");
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const [result, setResult] = useState<BulkResult | null>(null);
  const needsGroup = action === "addGroup" || action === "removeGroup";
  useEffect(() => { if (needsGroup) api.assignableGroups().then(setGroups).catch(() => {}); }, [needsGroup]);

  const n = dns.length;
  const who = `${n} ${n === 1 ? "person" : "people"}`;
  const run = async () => {
    setBusy(true); setErr("");
    try { setResult(await api.bulkUsers(action, dns, group, action === "delete")); } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
  };
  const ready = (!needsGroup || group !== "") && (action !== "delete" || typed.trim().toLowerCase() === "delete");

  if (result) {
    const bad = result.results.filter((r) => !r.ok);
    return (
      <Modal title={`${TITLES[action]}: finished`} onClose={() => { onDone(); onClose(); }}>
        <Notice kind={bad.length === 0 ? "ok" : "warn"}>Done for {result.done} of {n}.{bad.length > 0 && " These couldn't be changed:"}</Notice>
        {bad.length > 0 && <ul className="plainlist">{bad.map((r) => <li key={r.dn}><strong>{shortName(r.dn)}</strong>: {r.message}</li>)}</ul>}
        <div className="formactions"><button className="primary" onClick={() => { onDone(); onClose(); }}>Close</button></div>
      </Modal>
    );
  }
  return (
    <Modal title={`${TITLES[action]} ${who}?`} onClose={onClose}>
      {action === "suspend" && <p>They won't be able to sign in until you restore them, and anyone signed in is signed out. Nothing is deleted.</p>}
      {action === "restore" && <p>They'll be able to sign in again.</p>}
      {action === "delete" && <p>This permanently removes {who}, their group memberships and their personal groups. It can't be undone. If you only want to block sign-in, suspend them instead.</p>}
      {needsGroup && (
        <label>Group
          <select value={group} onChange={(e) => setGroup(e.target.value)}>
            <option value="">Choose a group…</option>
            {groups.map((g) => <option key={g.dn} value={g.dn}>{g.name}</option>)}
          </select>
        </label>
      )}
      {action === "delete" && <label>Type <strong>delete</strong> to confirm<input value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" /></label>}
      {err && <Notice kind="error">{err}</Notice>}
      <div className="formactions">
        <button className={action === "delete" ? "danger" : "primary"} disabled={!ready || busy} onClick={run}>{busy ? "Working…" : `${TITLES[action]} ${who}`}</button>
        <button onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}
