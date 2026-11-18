import { useEffect, useState } from "react";
import { api, MailSettings, ReminderStatus } from "./api";
import { Notice } from "./ui";

const PRESETS: Record<MailSettings["security"], { label: string; port: number; hint: string }> = {
  starttls: { label: "STARTTLS (usually port 587)", port: 587, hint: "Starts plain, then switches to an encrypted connection. The most common choice." },
  tls: { label: "TLS from the start (usually port 465)", port: 465, hint: "Encrypted from the first byte." },
  none: { label: "No encryption (this machine only)", port: 25, hint: "Only allowed for a mail server on this same machine, such as a local relay." },
};

/** The outgoing mail server: what Dapper uses to send password reset links and notices. */
export function Email() {
  const [m, setM] = useState<MailSettings | null>(null);
  const [rem, setRem] = useState<ReminderStatus | null>(null);
  const [remMsg, setRemMsg] = useState<{ kind: "ok" | "error"; text: string } | null>(null);
  const [err, setErr] = useState("");
  const [ok, setOk] = useState("");
  const [busy, setBusy] = useState(false);
  const [to, setTo] = useState("");
  const [testMsg, setTestMsg] = useState<{ kind: "ok" | "error"; text: string } | null>(null);

  useEffect(() => { api.mail().then((r) => { setM(r.mail); setRem(r.reminders); }).catch((x) => setErr((x as Error).message)); }, []);
  if (!m) return <div className="page">{err ? <Notice kind="error">{err}</Notice> : "Loading…"}</div>;

  const set = (p: Partial<MailSettings>) => { setM({ ...m, ...p }); setOk(""); };
  const pickSecurity = (security: MailSettings["security"]) => {
    const was = PRESETS[m.security].port;
    set({ security, port: m.port === was ? PRESETS[security].port : m.port });
  };

  const save = async () => {
    setErr(""); setOk(""); setBusy(true);
    try {
      const r = await api.saveMail(m);
      setM({ ...r.mail, password: "" });
      setOk(r.mail.enabled ? "Saved. Dapper can now send email." : "Saved. Email is turned off.");
    } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
  };

  const test = async () => {
    setTestMsg(null); setBusy(true);
    try { await api.testMail(m, to); setTestMsg({ kind: "ok", text: `A test message was sent to ${to}. Check that it arrived (and the spam folder).` }); }
    catch (x) { setTestMsg({ kind: "error", text: (x as Error).message }); } finally { setBusy(false); }
  };

  return (
    <div className="page narrow">
      <div className="pagehead"><h2>Email</h2></div>
      <p className="sub">Dapper sends email only for things people ask for or need to know, such as a link to choose a new password. Tell it which mail server to use. Nothing is sent until you turn email on below.</p>
      {err && <Notice kind="error">{err}</Notice>}
      {ok && <Notice kind="ok">{ok}</Notice>}

      <div className="panel">
        <label className="inline"><input type="checkbox" checked={m.enabled} onChange={(e) => set({ enabled: e.target.checked })} /> <strong>Send email</strong></label>

        <div className="grid2">
          <label>Mail server<input value={m.host} onChange={(e) => set({ host: e.target.value })} placeholder="smtp.example.org" autoCapitalize="none" spellCheck={false} /></label>
          <label>Port<input type="number" min={1} max={65535} value={m.port} onChange={(e) => set({ port: Number(e.target.value) })} /></label>
        </div>
        <label>Connection
          <select value={m.security} onChange={(e) => pickSecurity(e.target.value as MailSettings["security"])}>
            {Object.entries(PRESETS).map(([k, v]) => <option key={k} value={k}>{v.label}</option>)}
          </select>
          <span className="sub">{PRESETS[m.security].hint}</span>
        </label>
        <div className="grid2">
          <label>Username <span className="sub">(leave empty if the server needs none)</span>
            <input value={m.username} onChange={(e) => set({ username: e.target.value })} autoComplete="off" autoCapitalize="none" spellCheck={false} /></label>
          <label>Password
            <input type="password" autoComplete="new-password" value={m.password ?? ""} onChange={(e) => set({ password: e.target.value })}
              placeholder={m.passwordSet ? "Saved. Type to replace." : ""} disabled={!m.username} />
            {m.passwordSet && <span className="sub">Dapper forgets the saved password if you change the server, port or username.</span>}
          </label>
        </div>
        <div className="grid2">
          <label>Send from (address)<input type="email" value={m.from} onChange={(e) => set({ from: e.target.value })} placeholder="no-reply@example.org" autoCapitalize="none" /></label>
          <label>Sender name<input value={m.fromName} onChange={(e) => set({ fromName: e.target.value })} placeholder="Dapper" /></label>
        </div>
        <label>Dapper's web address
          <input value={m.publicURL} onChange={(e) => set({ publicURL: e.target.value })} placeholder="https://dapper.example.org" autoCapitalize="none" spellCheck={false} />
          <span className="sub">The address people type in their browser. Links in emails are built from this, so use the real one.</span>
        </label>
        <label className="inline"><input type="checkbox" checked={m.remindExpiry} onChange={(e) => set({ remindExpiry: e.target.checked })} /> Remind people by email before their password expires</label>
        <label className="inline"><input type="checkbox" checked={m.skipVerify} onChange={(e) => set({ skipVerify: e.target.checked })} /> Accept a mail server certificate that isn't trusted (not recommended)</label>

        <div className="formactions">
          <button className="primary" disabled={busy} onClick={save}>Save</button>
        </div>
      </div>

      <div className="panel">
        <h3>Send a test message</h3>
        <p className="sub">Uses the settings above, even if you haven't saved them yet.</p>
        <label>Send to<input type="email" value={to} onChange={(e) => setTo(e.target.value)} placeholder="you@example.org" autoCapitalize="none" /></label>
        {testMsg && <Notice kind={testMsg.kind}>{testMsg.text}</Notice>}
        <div className="formactions">
          <button disabled={busy || !to || !m.host} onClick={test}>Send test message</button>
        </div>
      </div>

      <div className="panel">
        <h3>Password expiry reminders</h3>
        <p className="sub">
          {rem?.rulesOn
            ? <>People with an email address get a message {rem.days.slice(0, -1).join(", ")} and {rem.days[rem.days.length - 1]} days before their password expires, once each. Dapper checks every few hours.</>
            : <>Passwords only expire when your directory's password rules include a maximum age. They aren't turned on, so nobody is reminded. You can turn them on from the Server page.</>}
        </p>
        {rem?.lastRun && <p className="sub">Last check: {new Date(rem.lastRun).toLocaleString()}, {rem.lastSent} reminder{rem.lastSent === 1 ? "" : "s"} sent.{rem.lastError ? ` It reported: ${rem.lastError}.` : ""}</p>}
        {remMsg && <Notice kind={remMsg.kind}>{remMsg.text}</Notice>}
        <div className="formactions">
          <button disabled={busy || !m.enabled || !rem?.rulesOn} onClick={async () => {
            setRemMsg(null); setBusy(true);
            try {
              const r = await api.runReminders();
              setRemMsg({ kind: "ok", text: r.sent === 0 ? "Checked. Nobody needed a reminder right now." : `Checked. ${r.sent} reminder${r.sent === 1 ? " was" : "s were"} sent.` });
              setRem((await api.mail()).reminders);
            } catch (x) { setRemMsg({ kind: "error", text: (x as Error).message }); } finally { setBusy(false); }
          }}>Check now</button>
        </div>
      </div>
    </div>
  );
}
