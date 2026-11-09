import { useState } from "react";
import { api, BundledResult, BundledStatus, SettingsIO, SetupAdmin } from "./api";
import { blankSettings, Patch, PasswordSessionFields, UnixFields } from "./SettingsParts";
import { Notice, SecretBox } from "./ui";
import { ConnectHelp } from "./Connect";

const STEPS = ["Your server", "Your directory", "Linux accounts", "Administrator", "Review"];
const COMPOSE = "docker compose -f docker-compose.yml -f docker-compose.server.yml up -d --build";

/** Setup of a new directory server that Dapper runs (the bundled OpenLDAP container). */
export function SetupNew({ onBack, onDone }: { onBack: () => void; onDone: () => void }) {
  const [step, setStep] = useState(0);
  const [status, setStatus] = useState<BundledStatus | null>(null);
  const [org, setOrg] = useState("");
  const [domain, setDomain] = useState("");
  const [s, setS] = useState<SettingsIO>(blankSettings());
  const [admin, setAdmin] = useState<SetupAdmin>({ mode: "new", username: "", givenName: "", sn: "", password: "" });
  const [features, setFeatures] = useState({ memberOf: true, refint: true });
  const [plan, setPlan] = useState<string[]>([]);
  const [suffix, setSuffix] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<BundledResult | null>(null);

  const patch: Patch = (section, values) => setS((cur) => ({ ...cur, [section]: { ...cur[section], ...values } }));
  const guard = async (fn: () => Promise<void>) => {
    setBusy(true); setErr("");
    try { await fn(); } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
  };
  const body = () => ({ organization: org, domain, settings: s, admin, features });

  const check = () => guard(async () => { setStatus(await api.bundledStatus()); });
  const review = () => guard(async () => {
    const p = await api.bundledPreview(body());
    setPlan(p.plan); setSuffix(p.suffix); setConfirmed(false); setStep(4);
  });
  const finish = () => guard(async () => { setResult(await api.bundledApply(body())); });

  if (result) return <Ready r={result} onDone={onDone} />;

  const ready = status?.found && status.reachable;
  const derived = domain.trim() ? domain.trim().toLowerCase().split(".").map((p) => "dc=" + p).join(",") : "";

  return (
    <div className="wizard">
      <h1>Set up a new directory server</h1>
      <ol className="steps" aria-label="Setup steps">
        {STEPS.map((n, i) => <li key={n} className={i === step ? "on" : i < step ? "done" : ""} aria-current={i === step ? "step" : undefined}>{n}</li>)}
      </ol>
      <div className="panel">
        {step === 0 && (
          <>
            <h2>Your server</h2>
            <p>Dapper comes with its own directory server (OpenLDAP). It runs next to Dapper in its own container and starts out empty. Dapper looks for it now.</p>
            {!status && <p className="sub">Click <strong>Look for the server</strong> to check.</p>}
            {status && ready && <Notice kind="ok">Found the new server and connected to it{status.host ? <> (it will be known to your other machines as <strong>{status.host}</strong>)</> : ""}.</Notice>}
            {status && !ready && (
              <>
                <Notice kind="warn">{status.message}</Notice>
                <p>If you haven't started the server yet, run this where you run Dapper, then check again:</p>
                <pre className="code">{COMPOSE}</pre>
              </>
            )}
            {status?.host === "localhost" && ready && (
              <Notice kind="warn">The server doesn't know a real name for itself yet (it's “localhost”), so other machines won't be able to check its certificate. Set <code>LDAP_HOSTNAME</code> in your .env file and recreate the server before continuing, or carry on if you're only trying it out.</Notice>
            )}
            {err && <Notice kind="error">{err}</Notice>}
            <div className="formactions">
              <button onClick={onBack}>Back</button>
              <button disabled={busy} onClick={check}>{busy ? "Looking…" : status ? "Check again" : "Look for the server"}</button>
              <button className="primary" disabled={!ready} onClick={() => setStep(1)}>Next</button>
            </div>
          </>
        )}

        {step === 1 && (
          <>
            <h2>Your directory</h2>
            <p className="sub">Name your organization and its domain. The domain becomes the name of your directory.</p>
            <label>Organization name<input value={org} onChange={(e) => setOrg(e.target.value)} placeholder="Example Inc." autoFocus /></label>
            <label>Domain name<input value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="example.org" autoCapitalize="none" spellCheck={false} />
              <span className="sub">{derived ? <>Your directory will be called <code>{derived}</code>.</> : "A domain you own, like example.org."}</span></label>
            {err && <Notice kind="error">{err}</Notice>}
            <div className="formactions">
              <button onClick={() => { setErr(""); setStep(0); }}>Back</button>
              <button className="primary" disabled={!org.trim() || !domain.includes(".")} onClick={() => { setErr(""); setStep(2); }}>Next</button>
            </div>
          </>
        )}

        {step === 2 && (
          <>
            <h2>Linux accounts</h2>
            <p className="sub">Will people in this directory log in to Linux servers or applications that use it?</p>
            <UnixFields s={s} patch={patch} schema={null} />
            <div className="formactions">
              <button onClick={() => setStep(1)}>Back</button>
              <button className="primary" onClick={() => setStep(3)}>Next</button>
            </div>
          </>
        )}

        {step === 3 && (
          <>
            <h2>Administrator</h2>
            <p className="sub">Create the first administrator. Administrators can manage everyone.</p>
            <label>Username<input value={admin.username} onChange={(e) => setAdmin({ ...admin, username: e.target.value })} autoComplete="off" autoCapitalize="none" /></label>
            <div className="grid2">
              <label>First name<input value={admin.givenName} onChange={(e) => setAdmin({ ...admin, givenName: e.target.value })} /></label>
              <label>Last name<input value={admin.sn} onChange={(e) => setAdmin({ ...admin, sn: e.target.value })} /></label>
            </div>
            <label>Password<input type="password" autoComplete="new-password" value={admin.password} onChange={(e) => setAdmin({ ...admin, password: e.target.value })} />
              <span className="sub">At least {s.password.minLength} characters.</span></label>
            <fieldset>
              <legend>Server features</legend>
              <label className="inline"><input type="checkbox" checked={features.memberOf} onChange={(e) => setFeatures({ ...features, memberOf: e.target.checked })} /> Show which groups each person belongs to (memberOf). Recommended.</label>
              <label className="inline"><input type="checkbox" checked={features.refint} onChange={(e) => setFeatures({ ...features, refint: e.target.checked })} /> Remove people from groups when they are deleted (referential integrity). Recommended.</label>
              <span className="sub">You can turn more features on later under Server.</span>
            </fieldset>
            <details className="advanced">
              <summary>Password and sign-out rules</summary>
              <PasswordSessionFields s={s} patch={patch} />
            </details>
            {err && <Notice kind="error">{err}</Notice>}
            <div className="formactions">
              <button onClick={() => { setErr(""); setStep(2); }}>Back</button>
              <button className="primary" disabled={busy || !admin.username || !admin.givenName || !admin.sn || admin.password.length < s.password.minLength} onClick={review}>Review</button>
            </div>
          </>
        )}

        {step === 4 && (
          <>
            <h2>Review</h2>
            <div className="plan">
              <h3>Dapper will make these changes on the new server</h3>
              <ul>{plan.map((p) => <li key={p}>{p}</li>)}</ul>
              <label className="inline"><input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /> Yes, set up <code>{suffix}</code></label>
            </div>
            {err && <Notice kind="error">{err}</Notice>}
            <div className="formactions">
              <button onClick={() => { setErr(""); setStep(3); }}>Back</button>
              <button className="primary" disabled={busy || !confirmed} onClick={finish}>{busy ? "Setting up…" : "Set up the server"}</button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

/** Shown once when the new server is set up: its passwords and how to connect to it. */
function Ready({ r, onDone }: { r: BundledResult; onDone: () => void }) {
  const host = r.host || "localhost";
  return (
    <div className="wizard">
      <div className="panel">
        <h2>Your directory server is ready</h2>
        <Notice kind="warn">Save these two passwords now. They are shown only this once. Dapper keeps the directory manager's password so it can work, but it never stores the configuration password.</Notice>
        <h3>Directory manager</h3>
        <p className="sub">Full control of the directory <code>{r.suffix}</code>. Sign in as <code>{r.managerDN}</code>.</p>
        <SecretBox value={r.managerPassword} />
        <h3>Configuration administrator</h3>
        <p className="sub">Needed for the Server page (features, certificates, access rules). Sign in as <code>{r.configDN}</code>.</p>
        <SecretBox value={r.configPassword} />

        <h3>Connecting your Linux servers and applications</h3>
        <ConnectHelp host={host} suffix={r.suffix} certificate={r.certificate} />

        <div className="formactions"><button className="primary" onClick={onDone}>Go to sign in</button></div>
      </div>
    </div>
  );
}
