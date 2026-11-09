import { useEffect, useState } from "react";
import { api, Me, setSessionEndedHandler } from "./api";
import { ThemePicker } from "./ThemePicker";
import { UserList } from "./UserList";
import { PersonDetail } from "./PersonDetail";
import { NewPerson } from "./NewPerson";
import { MyAccount } from "./MyAccount";
import { GroupDetail, Groups } from "./Groups";
import { Settings } from "./Settings";
import { Advanced } from "./Advanced";
import { Server } from "./Server";
import { Activity } from "./Activity";
import { ImportPeople } from "./ImportPeople";
import { Units } from "./Units";
import { Setup } from "./Setup";
import { go, useRoute } from "./router";

export function App() {
  const [me, setMe] = useState<Me | null>(null);
  const [checked, setChecked] = useState(false);
  const [configured, setConfigured] = useState(true);
  const [ended, setEnded] = useState(false);

  // Any request that finds the session gone sends the person back to sign in; the address
  // (which page they were on) is left alone, so they come back to it after signing in.
  useEffect(() => { setSessionEndedHandler(() => { setMe(null); setEnded(true); }); }, []);

  useEffect(() => {
    Promise.all([
      api.setupStatus().then((r) => setConfigured(r.configured)).catch(() => {}),
      api.me().then(setMe).catch(() => {}),
    ]).finally(() => setChecked(true));
  }, []);

  if (!checked) return null;
  if (!configured) return <Setup onDone={() => { setConfigured(true); go("/"); }} />;
  if (!me) return <Login ended={ended} onSignedIn={(m) => {
    setMe(m); setEnded(false);
    const here = decodeURIComponent(window.location.hash.replace(/^#/, "")) || "/";
    if (here === "/") go(m.isAdmin ? "/people" : "/me"); // otherwise stay on the page they were on
  }} />;
  return <Shell me={me} onSignOut={() => api.logout().then(() => { setMe(null); go("/"); })} />;
}

function VersionNote() {
  const [v, setV] = useState("");
  useEffect(() => { api.version().then((r) => setV(r.version)).catch(() => {}); }, []);
  return v ? <footer className="version">Dapper {v}</footer> : null;
}

function Login({ onSignedIn, ended }: { onSignedIn: (m: Me) => void; ended?: boolean }) {
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
        {ended && <p className="notice" role="status">Your session has ended. Please sign in again.</p>}
        <label>Username or email
          <input autoFocus autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} />
        </label>
        <label>Password
          <input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </label>
        {err && <p className="error" role="alert">{err}</p>}
        <button disabled={busy || !username || !password}>{busy ? "Signing in…" : "Sign in"}</button>
      </form>
      <VersionNote />
    </>
  );
}

const section = (path: string) => path.split("/")[1] ?? "";
const arg = (path: string) => path.split("/").slice(2).join("/");

function Shell({ me, onSignOut }: { me: Me; onSignOut: () => void }) {
  const path = useRoute();
  const sec = section(path);

  // Send people to a sensible home, and keep non-admins out of admin screens.
  const adminOnly = ["people", "person", "units", "groups", "group", "settings", "server", "activity", "advanced"];
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
  else if (sec === "people" && arg(path) === "import") view = <ImportPeople />;
  else if (sec === "people") view = <UserList />;
  else if (sec === "units") view = <Units />;
  else if (sec === "person") view = <PersonDetail key={arg(path)} dn={arg(path)} me={me} />;
  else if (sec === "groups") view = <Groups />;
  else if (sec === "group") view = <GroupDetail dn={arg(path)} />;
  else if (sec === "settings") view = <Settings />;
  else if (sec === "server") view = <Server />;
  else if (sec === "activity") view = <Activity />;
  else if (sec === "advanced") view = <Advanced />;
  else view = <MyAccount me={me} />;

  return (
    <div className="shell">
      <header>
        <strong>Dapper</strong>
        <nav aria-label="Main">
          {me.isAdmin && tab("/people", "People", ["people", "person", "units"])}
          {me.isAdmin && me.features.groups && tab("/groups", "Groups", ["groups", "group"])}
          {tab("/me", "My account", ["me", ""])}
          {me.isAdmin && tab("/settings", "Settings", ["settings"])}
          {me.isAdmin && tab("/server", "Server", ["server"])}
          {me.isAdmin && tab("/activity", "Activity", ["activity"])}
          {me.isAdmin && tab("/advanced", "Advanced", ["advanced"])}
        </nav>
        <span className="spacer" />
        <ThemePicker />
        <span>{me.name}{me.isAdmin ? " (admin)" : ""}</span>
        <button onClick={onSignOut}>Sign out</button>
      </header>
      <div className="content">{view}</div>
      <VersionNote />
    </div>
  );
}
