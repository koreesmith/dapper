import { useState } from "react";
import { api, ApiError, Check, Discovery, SetupAdmin, SettingsIO, setSetupCode } from "./api";
import { AdminGroupField, blankSettings, Checks, ConnectionFields, LayoutFields, PasswordSessionFields, Patch, UnixFields } from "./SettingsParts";
import { Notice } from "./ui";
import { ThemePicker } from "./ThemePicker";

const STEPS = ["Setup code", "Your directory", "Where things live", "Linux accounts", "Administrators", "Review"];

export function Setup({ onDone }: { onDone: () => void }) {
  const [step, setStep] = useState(0);
  const [code, setCode] = useState("");
  const [s, setS] = useState<SettingsIO>(blankSettings());
  const [createContainers, setCreate] = useState(true);
  const [admin, setAdmin] = useState<SetupAdmin>({ mode: "existing", username: "", givenName: "", sn: "", password: "" });
  const [checks, setChecks] = useState<Check[]>([]);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [suggested, setSuggested] = useState(false);
  const [done, setDone] = useState(false);
  const [detected, setDetected] = useState<Discovery["unix"] | null>(null);

  const patch: Patch = (section, values) => setS((cur) => ({ ...cur, [section]: { ...cur[section], ...values } }));
  const none = {};
  const fail = (x: unknown) => {
    setErr((x as Error).message);
    const d = (x as ApiError).data as { checks?: Check[] } | undefined;
    if (d?.checks) setChecks(d.checks);
  };

  const guard = async (fn: () => Promise<void>) => {
    setBusy(true); setErr("");
    try { await fn(); } catch (x) { fail(x); } finally { setBusy(false); }
  };

  const willCreate = (c: Check) =>
    (createContainers && !!c.missing) || (c.key === "admin" && admin.mode !== "existing");

  const connect = () => guard(async () => {
    const d = await api.setupDiscover(s);
    setChecks(d.checks);
    if (!d.ok) return;
    if (d.unix && !suggested) {
      setDetected(d.unix);
      if (d.unix.detected) {
        setS((cur) => ({ ...cur, unix: {
          ...cur.unix, enabled: true,
          homePattern: d.unix!.homePattern || cur.unix.homePattern,
          defaultShell: d.unix!.defaultShell || cur.unix.defaultShell,
        } }));
      }
    }
    if (d.suggestions && !suggested) {
      setSuggested(true);
      setS((cur) => ({
        ...cur,
        users: { ...cur.users, baseDN: cur.users.baseDN || d.suggestions!.usersBaseDN, suspendedBaseDN: cur.users.suspendedBaseDN || d.suggestions!.suspendedBaseDN },
        groups: { ...cur.groups, baseDN: cur.groups.baseDN || d.suggestions!.groupsBaseDN },
        admin: { groupDN: cur.admin.groupDN || d.suggestions!.adminGroupDN },
      }));
    }
    setStep(2);
  });

  const review = () => guard(async () => { setChecks((await api.setupCheck(s)).checks); setStep(5); });

  const finish = () => guard(async () => {
    await api.setupComplete({ settings: s, createContainers, admin });
    setDone(true);
  });

  const reviewOK = checks.length > 0 && checks.every((c) => c.ok || willCreate(c));

  if (done) {
    return (
      <div className="wizard">
        <div className="panel">
          <h2>Dapper is ready</h2>
          <p>Your settings were saved. You can change any of them later under <strong>Settings</strong>.</p>
          <p>Now sign in with an administrator account{admin.mode === "new" ? <> — the one you just created: <strong>{admin.username}</strong></> : admin.mode === "person" ? <>: <strong>{admin.username}</strong></> : ""}.</p>
          <div className="formactions"><button className="primary" onClick={onDone}>Go to sign in</button></div>
        </div>
      </div>
    );
  }

  return (
    <div className="wizard">
      <div className="corner"><ThemePicker /></div>
      <h1>Set up Dapper</h1>
      <ol className="steps" aria-label="Setup steps">
        {STEPS.map((n, i) => <li key={n} className={i === step ? "on" : i < step ? "done" : ""} aria-current={i === step ? "step" : undefined}>{n}</li>)}
      </ol>

      <div className="panel">
        {step === 0 && (
          <>
            <h2>Welcome</h2>
            <p>Dapper manages the people and groups in your LDAP directory. This short setup connects it to your server. Nothing is installed into your directory.</p>
            <p>To make sure only you can do this, enter the <strong>setup code</strong> from Dapper's startup log. If you started it with Docker Compose, run:</p>
            <pre className="code">docker compose logs dapper</pre>
            <label>Setup code<input value={code} onChange={(e) => setCode(e.target.value)} autoFocus autoComplete="off" /></label>
            {err && <Notice kind="error">{err}</Notice>}
            <div className="formactions">
              <button className="primary" disabled={!code.trim() || busy} onClick={() => guard(async () => {
                setSetupCode(code.trim());
                // A harmless call that checks the code before moving on.
                await api.setupCheck(blankSettings());
                setStep(1);
              })}>Continue</button>
            </div>
          </>
        )}

        {step === 1 && (
          <>
            <h2>Your directory</h2>
            <p className="sub">Where is your LDAP server, and which account should Dapper use?</p>
            <ConnectionFields s={s} patch={patch} locked={none} />
            {checks.length > 0 && <Checks checks={checks} />}
            {err && <Notice kind="error">{err}</Notice>}
            <div className="formactions">
              <button onClick={() => { setErr(""); setChecks([]); setStep(0); }}>Back</button>
              <button className="primary" disabled={busy || !s.ldap.url || !s.ldap.bindDN || !s.ldap.bindPassword} onClick={connect}>{busy ? "Connecting…" : "Connect"}</button>
            </div>
          </>
        )}

        {step === 2 && (
          <>
            <h2>Where things live</h2>
            <p className="sub">Connected. We've suggested names based on your directory; change them to match yours.</p>
            <LayoutFields s={s} patch={patch} locked={none} />
            <label className="inline"><input type="checkbox" checked={createContainers} onChange={(e) => setCreate(e.target.checked)} /> Create these containers if they don't exist yet</label>
            {err && <Notice kind="error">{err}</Notice>}
            <div className="formactions">
              <button onClick={() => { setErr(""); setStep(1); }}>Back</button>
              <button className="primary" disabled={!s.users.baseDN} onClick={() => { setErr(""); setStep(3); }}>Next</button>
            </div>
          </>
        )}

        {step === 3 && (
          <>
            <h2>Linux accounts</h2>
            <p className="sub">Do the people in this directory log in to Linux servers or applications that use it?</p>
            {detected?.detected && (
              <p className="found">Found {detected.accounts} existing Linux account{detected.accounts === 1 ? "" : "s"} (highest user ID in use: {detected.highestUID}). We've matched your existing home directory and shell settings; new accounts will be numbered after the highest ID within the range below.</p>
            )}
            {detected?.detected && detected.highestUID > 0 && detected.highestUID < s.unix.uidMin && (
              <p className="sub">Your existing IDs stop at {detected.highestUID}, but the range below starts at {s.unix.uidMin}.{" "}
                <button type="button" className="link" onClick={() => patch("unix", { uidMin: detected.highestUID + 1, uidMax: Math.max(s.unix.uidMax, detected.highestUID + 1000) })}>
                  Start new accounts at {detected.highestUID + 1} instead</button></p>
            )}
            <UnixFields s={s} patch={patch} />
            {s.unix.enabled && !s.groups.baseDN && <Notice kind="error">Linux accounts need a groups container (go back one step), because each person gets a private group there.</Notice>}
            <div className="formactions">
              <button onClick={() => { setErr(""); setStep(2); }}>Back</button>
              <button className="primary" disabled={s.unix.enabled && !s.groups.baseDN} onClick={() => { setErr(""); setStep(4); }}>Next</button>
            </div>
          </>
        )}

        {step === 4 && (
          <>
            <h2>Administrators</h2>
            <p className="sub">Administrators can manage everyone. Who should be the first one?</p>
            <fieldset>
              <legend>Administrators group</legend>
              <label className="inline"><input type="radio" checked={admin.mode === "existing"} onChange={() => setAdmin({ ...admin, mode: "existing" })} /> Use a group that already exists</label>
              <label className="inline"><input type="radio" checked={admin.mode === "person"} onChange={() => setAdmin({ ...admin, mode: "person" })} /> Make an existing person an administrator (creates the group if needed)</label>
              <label className="inline"><input type="radio" checked={admin.mode === "new"} onChange={() => setAdmin({ ...admin, mode: "new" })} /> Create a brand-new administrator account (creates the group if needed)</label>
            </fieldset>
            <AdminGroupField s={s} patch={patch} locked={none} />
            {admin.mode !== "existing" && (
              <label>Username<input value={admin.username} onChange={(e) => setAdmin({ ...admin, username: e.target.value })} autoComplete="off" /></label>
            )}
            {admin.mode === "new" && (
              <>
                <div className="grid2">
                  <label>First name<input value={admin.givenName} onChange={(e) => setAdmin({ ...admin, givenName: e.target.value })} /></label>
                  <label>Last name<input value={admin.sn} onChange={(e) => setAdmin({ ...admin, sn: e.target.value })} /></label>
                </div>
                <label>Password<input type="password" autoComplete="new-password" value={admin.password} onChange={(e) => setAdmin({ ...admin, password: e.target.value })} />
                  <span className="sub">At least {s.password.minLength} characters.</span></label>
              </>
            )}
            <details className="advanced">
              <summary>Password and sign-out rules</summary>
              <PasswordSessionFields s={s} patch={patch} />
            </details>
            {err && <Notice kind="error">{err}</Notice>}
            <div className="formactions">
              <button onClick={() => { setErr(""); setStep(3); }}>Back</button>
              <button className="primary" disabled={busy || !s.admin.groupDN || (admin.mode !== "existing" && !admin.username) ||
                (admin.mode === "new" && (!admin.givenName || !admin.sn || !admin.password))} onClick={review}>Review</button>
            </div>
          </>
        )}

        {step === 5 && (
          <>
            <h2>Review</h2>
            <p className="sub">Dapper tested these settings against your directory.</p>
            <Checks checks={checks} pending={willCreate} />
            {admin.mode !== "existing" && <p className="sub">The administrators group{admin.mode === "new" ? ", the new account," : ""} and {admin.username ? <strong>{admin.username}</strong> : "the person"} will be set up when you finish.</p>}
            {err && <Notice kind="error">{err}</Notice>}
            <div className="formactions">
              <button onClick={() => { setErr(""); setStep(4); }}>Back</button>
              <button className="primary" disabled={busy || !reviewOK} onClick={finish}>{busy ? "Finishing…" : "Finish setup"}</button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
