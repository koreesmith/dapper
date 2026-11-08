import { useEffect, useState } from "react";
import { api, Attrs, GroupSummary, Me } from "./api";
import { Notice, SecretBox } from "./ui";
import { href } from "./router";

const slug = (s: string) => s.toLowerCase().normalize("NFD").replace(/[̀-ͯ]/g, "").replace(/[^a-z0-9]+/g, "");

export function NewPerson({ me }: { me: Me }) {
  const [first, setFirst] = useState("");
  const [last, setLast] = useState("");
  const [username, setUsername] = useState("");
  const [usernameEdited, setUsernameEdited] = useState(false);
  const [email, setEmail] = useState("");
  const [phone, setPhone] = useState("");
  const [title, setTitle] = useState("");
  const [mode, setMode] = useState<"generate" | "set">("generate");
  const [password, setPassword] = useState("");
  const [groups, setGroups] = useState<GroupSummary[]>([]);
  const [chosen, setChosen] = useState<string[]>([]);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<{ dn: string; username: string; pw?: string } | null>(null);

  useEffect(() => { if (me.features.groups) api.groups().then(setGroups).catch(() => {}); }, [me.features.groups]);
  useEffect(() => {
    if (!usernameEdited) setUsername(first && last ? `${slug(first)}.${slug(last)}` : slug(first || last));
  }, [first, last, usernameEdited]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true); setErr("");
    const attrs: Attrs = { givenName: [first], sn: [last] };
    if (email) attrs.mail = [email];
    if (phone) attrs.telephoneNumber = [phone];
    if (title) attrs.title = [title];
    try {
      const r = await api.createPerson({ username, attrs, password: mode === "set" ? password : undefined, groups: chosen });
      setCreated({ dn: r.dn, username: r.username, pw: r.temporaryPassword });
    } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
  };

  if (created) {
    return (
      <div className="page narrow">
        <div className="panel">
          <h2>{first} {last} was added</h2>
          {created.pw ? (
            <>
              <p>Give them this temporary password so they can sign in as <strong>{created.username}</strong>. It won't be shown again.</p>
              <SecretBox value={created.pw} />
            </>
          ) : <p>They can sign in as <strong>{created.username}</strong> with the password you set.</p>}
          <div className="formactions">
            <a className="btn primary" href={href("#/person", created.dn)}>View account</a>
            <a className="btn" href="#/people" onClick={() => setCreated(null)}>Back to people</a>
            <button onClick={() => { setCreated(null); setFirst(""); setLast(""); setEmail(""); setPhone(""); setTitle(""); setPassword(""); setUsernameEdited(false); setChosen([]); }}>Add another</button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="page narrow">
      <p><a href="#/people">← Back to people</a></p>
      <h2>New person</h2>
      <form className="panel" onSubmit={submit}>
        <div className="grid2">
          <label>First name *<input value={first} onChange={(e) => setFirst(e.target.value)} required autoFocus /></label>
          <label>Last name *<input value={last} onChange={(e) => setLast(e.target.value)} required /></label>
        </div>
        <label>Username *
          <input value={username} onChange={(e) => { setUsername(e.target.value.toLowerCase()); setUsernameEdited(true); }} required pattern="[a-z0-9][a-z0-9._\-]{1,31}"
            title="2–32 characters: lowercase letters, numbers, dots, dashes or underscores" />
          <span className="sub">What they type to sign in. Suggested from their name.</span>
        </label>
        <div className="grid2">
          <label>Email<input type="email" value={email} onChange={(e) => setEmail(e.target.value)} /></label>
          <label>Phone<input type="tel" value={phone} onChange={(e) => setPhone(e.target.value)} /></label>
        </div>
        <label>Job title<input value={title} onChange={(e) => setTitle(e.target.value)} /></label>

        {groups.length > 0 && (
          <fieldset>
            <legend>Groups</legend>
            {groups.map((g) => (
              <label className="inline" key={g.dn}>
                <input type="checkbox" checked={chosen.includes(g.dn)}
                  onChange={(e) => setChosen(e.target.checked ? [...chosen, g.dn] : chosen.filter((x) => x !== g.dn))} /> {g.name}{g.admin && <span className="sub"> — administrators: can manage everyone</span>}
              </label>
            ))}
          </fieldset>
        )}

        <fieldset>
          <legend>Password</legend>
          <label className="inline"><input type="radio" checked={mode === "generate"} onChange={() => setMode("generate")} /> Generate a temporary password for me</label>
          <label className="inline"><input type="radio" checked={mode === "set"} onChange={() => setMode("set")} /> I'll choose one</label>
          {mode === "set" && (
            <label>Password (at least {me.features.passwordMinLength} characters)
              <input type="text" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="off" minLength={me.features.passwordMinLength} required />
            </label>
          )}
        </fieldset>

        {err && <Notice kind="error">{err}</Notice>}
        <div className="formactions">
          <button className="primary" disabled={busy}>{busy ? "Creating…" : "Create account"}</button>
          <a className="btn" href="#/people">Cancel</a>
        </div>
      </form>
    </div>
  );
}
