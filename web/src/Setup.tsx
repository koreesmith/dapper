import { useEffect, useRef, useState } from "react";
import { api, ApiError, Check, Discovery, FoundGroup, FoundPerson, SetupAdmin, SettingsIO, setSetupCode } from "./api";
import { AdminGroupField, blankSettings, Checks, ConnectionFields, LayoutFields, PasswordSessionFields, Patch, UnixFields } from "./SettingsParts";
import { Notice } from "./ui";
import { ThemePicker } from "./ThemePicker";
import { SetupNew } from "./SetupNew";

const STEPS = ["Setup code", "Your directory", "Where things live", "Linux accounts", "Administrators", "Review"];
const FIX_STEP: Record<string, number> = { connect: 1, people: 2, groups: 2, suspended: 2, unix: 3, admin: 4 };

export function Setup({ onDone }: { onDone: () => void }) {
  const [step, setStep] = useState(0);
  const [code, setCode] = useState("");
  const [s, setS] = useState<SettingsIO>(blankSettings());
  const [admin, setAdmin] = useState<SetupAdmin>({ mode: "existing", username: "", givenName: "", sn: "", password: "" });
  const [checks, setChecks] = useState<Check[]>([]);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [suggested, setSuggested] = useState(false);
  const [done, setDone] = useState(false);
  const [detected, setDetected] = useState<Discovery["unix"] | null>(null);
  const [schema, setSchema] = useState<Record<string, boolean> | null>(null);
  const [found, setFound] = useState<string[]>([]);
  const [adminAuto, setAdminAuto] = useState(true); // admin group follows the groups container until edited
  const [groups, setGroups] = useState<FoundGroup[]>([]);
  const [people, setPeople] = useState<FoundPerson[]>([]);
  const [confirmed, setConfirmed] = useState(false);
  const [mode, setMode] = useState<"" | "existing" | "new">(""); // asked after the setup code
  const seq = useRef(0);

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

  // Keep the suggested administrators group in the groups container the person chose.
  useEffect(() => {
    if (adminAuto && s.groups.baseDN) setS((cur) => ({ ...cur, admin: { groupDN: "cn=dapper-admins," + cur.groups.baseDN } }));
  }, [s.groups.baseDN, adminAuto]);

  // On the Administrators step, look up the groups that already exist in the chosen container.
  useEffect(() => {
    if (step !== 4 || !s.groups.baseDN) return;
    api.setupGroups(s).then((r) => {
      setGroups(r.groups);
      if (r.suggest.objectClass && r.suggest.memberAttribute) {
        setS((cur) => ({ ...cur, groups: { ...cur.groups, objectClass: r.suggest.objectClass!, memberAttribute: r.suggest.memberAttribute! } }));
      }
    }).catch(() => setGroups([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step, s.groups.baseDN]);

  // Find existing people as the first administrator's username is typed.
  useEffect(() => {
    if (step !== 4 || admin.mode !== "person" || admin.username.trim().length < 2) { setPeople([]); return; }
    const mine = ++seq.current;
    const t = setTimeout(() => {
      api.setupPeople(s, admin.username.trim()).then((r) => { if (mine === seq.current) setPeople(r.people); }).catch(() => {});
    }, 250);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step, admin.mode, admin.username]);

  // Anything missing is created only after the person approves it on the last step.
  const willCreate = (c: Check) => !!c.missing || (c.key === "admin" && admin.mode !== "existing");

  const connect = () => guard(async () => {
    const d = await api.setupDiscover(s);
    setChecks(d.checks);
    if (!d.ok) return;
    setSchema(d.schema ?? null);
    setFound(d.containers ?? []);
    if (d.schema) {
      // Never carry forward an option this server can't support.
      setS((cur) => ({ ...cur, unix: {
        ...cur.unix,
        sshKeys: cur.unix.sshKeys && d.schema!.ldapPublicKey !== false,
        shadow: cur.unix.shadow && d.schema!.shadowAccount !== false,
        enabled: cur.unix.enabled && d.schema!.posixAccount !== false && d.schema!.posixGroup !== false,
      } }));
    }
    if (d.suggestions && d.exists && d.exists.adminGroupDN === false && !suggested) {
      setAdmin((a) => (a.mode === "existing" ? { ...a, mode: "person" } : a));
    }
    if (d.unix && !suggested) {
      setDetected(d.unix);
      if (d.unix.detected) {
        setS((cur) => ({ ...cur, unix: {
          ...cur.unix, enabled: d.schema?.posixAccount !== false,
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

  const review = () => guard(async () => { setChecks((await api.setupCheck(s)).checks); setConfirmed(false); setStep(5); });

  const finish = () => guard(async () => {
    await api.setupComplete({ settings: s, createContainers: true, confirmed, admin });
    setDone(true);
  });

  // What Dapper would create, in plain words, for the person to approve.
  const adminCheck = checks.find((c) => c.key === "admin");
  const plan: string[] = [
    ...checks.filter((c) => c.missing).map((c) => `Create the empty container ${c.missing}`),
    ...(admin.mode === "new" ? [`Create the account “${admin.username}” (${admin.givenName} ${admin.sn}) as the first administrator`] : []),
    ...(admin.mode !== "existing" && adminCheck && !adminCheck.ok ? [`Create the administrators group ${s.admin.groupDN}`] : []),
    ...(admin.mode !== "existing" ? [`Make “${admin.username}” a member of ${s.admin.groupDN}`] : []),
  ];
  const reviewOK = checks.length > 0 && checks.every((c) => c.ok || willCreate(c));
  const canFinish = reviewOK && (plan.length === 0 || confirmed);

  if (done) {
    return (
      <div className="wizard">
        <div className="panel">
          <h2>Dapper is ready</h2>
          <p>Your settings were saved. You can change any of them later under <strong>Settings</strong>.</p>
          <p>Now sign in with an administrator account{admin.mode !== "existing" && admin.username ? <>: <strong>{admin.username}</strong></> : ""}.</p>
          <div className="formactions"><button className="primary" onClick={onDone}>Go to sign in</button></div>
        </div>
      </div>
    );
  }

  if (step >= 1 && mode === "new") {
    return <><div className="corner"><ThemePicker /></div><SetupNew onBack={() => { setMode(""); }} onDone={onDone} /></>;
  }
  if (step === 1 && mode === "") {
    return (
      <div className="wizard">
        <div className="corner"><ThemePicker /></div>
        <h1>Set up Dapper</h1>
        <div className="panel">
          <h2>Do you already have an LDAP server?</h2>
          <p className="sub">Dapper can manage a directory you already run, or run a new one for you.</p>
          <div className="choices">
            <button type="button" className="choice" onClick={() => setMode("existing")}>
              <strong>I already have an LDAP server</strong>
              <span className="sub">Connect Dapper to it. Nothing about your server changes unless you approve it.</span>
            </button>
            <button type="button" className="choice" onClick={() => setMode("new")}>
              <strong>I need a new one</strong>
              <span className="sub">Dapper runs a new OpenLDAP server for you, in a container next to Dapper, and sets it up with you.</span>
            </button>
          </div>
          <div className="formactions"><button onClick={() => setStep(0)}>Back</button></div>
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
            <p>Dapper manages the people and groups in your LDAP directory. This short setup connects it to your server and looks at what's already there, so you only change what you need to. Nothing is created or changed in your directory without your approval on the last step.</p>
            <p>To make sure only you can do this, enter the <strong>setup code</strong> from Dapper's startup log. If you started it with Docker Compose, run:</p>
            <pre className="code">docker compose logs dapper</pre>
            <label>Setup code<input value={code} onChange={(e) => setCode(e.target.value)} autoFocus autoComplete="off" /></label>
            {err && <Notice kind="error">{err}</Notice>}
            <div className="formactions">
              <button className="primary" disabled={!code.trim() || busy} onClick={() => guard(async () => {
                setSetupCode(code.trim());
                await api.setupCheck(blankSettings()); // a harmless call that checks the code before moving on
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
              <button onClick={() => { setErr(""); setChecks([]); setMode(""); }}>Back</button>
              <button className="primary" disabled={busy || !s.ldap.url || !s.ldap.bindDN || !s.ldap.bindPassword} onClick={connect}>{busy ? "Connecting…" : "Connect"}</button>
            </div>
          </>
        )}

        {step === 2 && (
          <>
            <h2>Where things live</h2>
            <p className="sub">Connected. Dapper looked at your directory and picked the containers that look right; click any name under a box to use a different one. If a container doesn't exist, you'll be asked to approve creating it on the last step.</p>
            <LayoutFields s={s} patch={patch} locked={none} found={found} />
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
            <UnixFields s={s} patch={patch} schema={schema} />
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
            {groups.length > 0 && (
              <div className="found">Groups already in your directory:{" "}
                {groups.map((g) => (
                  <button key={g.dn} type="button" className="chip" onClick={() => { setAdminAuto(false); setAdmin({ ...admin, mode: "existing" }); patch("admin", { groupDN: g.dn }); }}>
                    {g.name} ({g.members})</button>
                ))}
                <div className="sub">Click one to use it as the administrators group. Everyone in it will be able to manage all accounts.</div>
              </div>
            )}
            <fieldset>
              <legend>How should administrators be set up?</legend>
              <label className="inline"><input type="radio" checked={admin.mode === "existing"} onChange={() => setAdmin({ ...admin, mode: "existing" })} /> Use a group that already exists</label>
              <label className="inline"><input type="radio" checked={admin.mode === "person"} onChange={() => setAdmin({ ...admin, mode: "person" })} /> Make an existing person an administrator (creates the group if needed)</label>
              <label className="inline"><input type="radio" checked={admin.mode === "new"} onChange={() => setAdmin({ ...admin, mode: "new" })} /> Create a brand-new administrator account (creates the group if needed)</label>
            </fieldset>
            <AdminGroupField s={s} patch={(sec, v) => { setAdminAuto(false); patch(sec, v); }} locked={none} />
            {admin.mode === "existing" && <p className="sub">This group must already exist and have at least one member. If it doesn't, choose one of the other two options and Dapper will offer to create it.</p>}
            {admin.mode !== "existing" && (
              <label>Username
                <input value={admin.username} onChange={(e) => setAdmin({ ...admin, username: e.target.value })} autoComplete="off" />
                {admin.mode === "person" && people.length > 0 && (
                  <span className="found-list">People found:{" "}
                    {people.map((p) => <button key={p.dn} type="button" className="chip" onClick={() => setAdmin({ ...admin, username: p.username })}>{p.name || p.username} ({p.username})</button>)}</span>
                )}
                {admin.mode === "person" && admin.username.trim().length >= 2 && people.length === 0 && <span className="sub">No one matching that was found in the people container.</span>}
              </label>
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
            <Checks checks={checks} pending={willCreate} onFix={(c) => { setErr(""); setStep(FIX_STEP[c.key] ?? 2); }} />
            {plan.length > 0 && (
              <div className="plan">
                <h3>Dapper will make these changes in your directory</h3>
                <ul>{plan.map((p) => <li key={p}>{p}</li>)}</ul>
                <label className="inline"><input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> Yes, make these changes</label>
              </div>
            )}
            {plan.length === 0 && reviewOK && <p className="sub">Everything you chose already exists, so Dapper won't create or change anything in your directory.</p>}
            {err && <Notice kind="error">{err}</Notice>}
            <div className="formactions">
              <button onClick={() => { setErr(""); setStep(4); }}>Back</button>
              <button className="primary" disabled={busy || !canFinish} onClick={finish}>{busy ? "Finishing…" : "Finish setup"}</button>
              {!reviewOK && <span className="error">Fix the items marked ✗ to finish.</span>}
              {reviewOK && plan.length > 0 && !confirmed && <span className="sub">Tick the box above to approve.</span>}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
