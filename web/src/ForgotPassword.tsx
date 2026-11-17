import { useEffect, useState } from "react";
import { api } from "./api";
import { go } from "./router";
import { ThemePicker } from "./ThemePicker";

const Back = () => <a href="#/" onClick={() => go("/")}>Back to sign in</a>;

/** "I forgot my password": ask for an emailed link. The answer is the same whether or not the account exists. */
export function ForgotPassword() {
  const [who, setWho] = useState("");
  const [err, setErr] = useState("");
  const [done, setDone] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <>
      <div className="corner"><ThemePicker /></div>
      <form className="card" onSubmit={async (e) => {
        e.preventDefault(); setBusy(true); setErr("");
        try { setDone((await api.forgotPassword(who)).message); } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
      }}>
        <h1>Forgot your password?</h1>
        {done ? (
          <>
            <p role="status">{done}</p>
            <p className="sub">Nothing arrived after a few minutes? Check the spam folder, or ask an administrator to reset it for you.</p>
          </>
        ) : (
          <>
            <p className="sub">Enter your username or email address. If we can reach you by email, we'll send a link to choose a new password.</p>
            <label>Username or email
              <input autoFocus autoComplete="username" value={who} onChange={(e) => setWho(e.target.value)} />
            </label>
            {err && <p className="error" role="alert">{err}</p>}
            <button disabled={busy || !who.trim()}>{busy ? "Sending…" : "Email me a link"}</button>
          </>
        )}
        <Back />
      </form>
    </>
  );
}

/** The page the emailed link opens: choose a new password. */
export function ResetPassword({ token }: { token: string }) {
  const [state, setState] = useState<"checking" | "ok" | "bad" | "done">("checking");
  const [minLength, setMinLength] = useState(8);
  const [pw, setPw] = useState("");
  const [again, setAgain] = useState("");
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api.checkReset(token).then((r) => { setMinLength(r.minLength); setState("ok"); }).catch((x) => { setErr((x as Error).message); setState("bad"); });
  }, [token]);

  const mismatch = again !== "" && pw !== again;
  return (
    <>
      <div className="corner"><ThemePicker /></div>
      <form className="card" onSubmit={async (e) => {
        e.preventDefault(); setBusy(true); setErr("");
        try { await api.resetByLink(token, pw); setState("done"); } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
      }}>
        <h1>Choose a new password</h1>
        {state === "checking" && <p>Checking your link…</p>}
        {state === "bad" && <><p className="error" role="alert">{err}</p><a href="#/forgot" onClick={() => go("/forgot")}>Ask for a new link</a></>}
        {state === "done" && <p role="status">Your password was changed. Sign in with it now. Anywhere you were signed in before has been signed out.</p>}
        {state === "ok" && (
          <>
            <label>New password
              <input type="password" autoFocus autoComplete="new-password" value={pw} onChange={(e) => setPw(e.target.value)} />
              <span className="sub">At least {minLength} characters.</span>
            </label>
            <label>Type it again
              <input type="password" autoComplete="new-password" value={again} onChange={(e) => setAgain(e.target.value)} aria-invalid={mismatch} />
            </label>
            {mismatch && <p className="error">The two passwords don't match.</p>}
            {err && <p className="error" role="alert">{err}</p>}
            <button disabled={busy || pw.length < minLength || pw !== again}>{busy ? "Saving…" : "Save new password"}</button>
          </>
        )}
        {state !== "bad" && <Back />}
      </form>
    </>
  );
}
