import { useEffect, useState } from "react";
import { api, Me } from "./api";
import { ThemePicker } from "./ThemePicker";
import { UserList } from "./UserList";
import { PersonDetail } from "./PersonDetail";
import { NewPerson } from "./NewPerson";
import { MyAccount } from "./MyAccount";
import { GroupDetail, Groups } from "./Groups";
import { Settings } from "./Settings";
import { Advanced } from "./Advanced";
import { Setup } from "./Setup";
import { go, useRoute } from "./router";

export function App() {
  const [me, setMe] = useState<Me | null>(null);
  const [checked, setChecked] = useState(false);
  const [configured, setConfigured] = useState(true);

  useEffect(() => {
    Promise.all([
      api.setupStatus().then((r) => setConfigured(r.configured)).catch(() => {}),
      api.me().then(setMe).catch(() => {}),
    ]).finally(() => setChecked(true));
  }, []);

  if (!checked) return null;
  if (!configured) return <Setup onDone={() => { setConfigured(true); go("/"); }} />;
  if (!me) return <Login onSignedIn={(m) => { setMe(m); go(m.isAdmin ? "/people" : "/me"); }} />;
  return <Shell me={me} onSignOut={() => api.logout().then(() => { setMe(null); go("/"); })} />;
}

function Login({ onSignedIn }: { onSignedIn: (m: Me) => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  return (
    <>
      <div className="corner"><ThemePicker /></div>
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
    </>
  );
}

const section = (path: string) => path.split("/")[1] ?? "";
const arg = (path: string) => path.split("/").slice(2).join("/");

function Shell({ me, onSignOut }: { me: Me; onSignOut: () => void }) {
  const path = useRoute();
  const sec = section(path);

  // Send people to a sensible home, and keep non-admins out of admin screens.
  const adminOnly = ["people", "person", "groups", "group", "settings", "advanced"];
  useEffect(() => {
    if (path === "/" || path === "") go(me.isAdmin ? "/people" : "/me");
    else if (!me.isAdmin && adminOnly.includes(sec)) go("/me");
  }, [path, me.isAdmin, sec]);

  const tab = (to: string, label: string, match: string[]) => (
    <a className={match.includes(sec) ? "tab on" : "tab"} href={"#" + to} aria-current={match.includes(sec) ? "page" : undefined}>{label}</a>
  );

  let view;
  if (!me.isAdmin && adminOnly.includes(sec)) view = null;
  else if (sec === "people" && arg(path) === "new") view = <NewPerson me={me} />;
  else if (sec === "people") view = <UserList />;
  else if (sec === "person") view = <PersonDetail key={arg(path)} dn={arg(path)} me={me} />;
  else if (sec === "groups") view = <Groups />;
  else if (sec === "group") view = <GroupDetail dn={arg(path)} />;
  else if (sec === "settings") view = <Settings />;
  else if (sec === "advanced") view = <Advanced />;
  else view = <MyAccount me={me} />;

  return (
    <div className="shell">
      <header>
        <strong>Dapper</strong>
        <nav aria-label="Main">
          {me.isAdmin && tab("/people", "People", ["people", "person"])}
          {me.isAdmin && me.features.groups && tab("/groups", "Groups", ["groups", "group"])}
          {tab("/me", "My account", ["me", ""])}
          {me.isAdmin && tab("/settings", "Settings", ["settings"])}
          {me.isAdmin && tab("/advanced", "Advanced", ["advanced"])}
        </nav>
        <span className="spacer" />
        <ThemePicker />
        <span>{me.name}{me.isAdmin ? " (admin)" : ""}</span>
        <button onClick={onSignOut}>Sign out</button>
      </header>
      <div className="content">{view}</div>
    </div>
  );
}
