// Browser test of "forgot password" against the demo directory (alice / alicepass is an administrator).
// Dapper must be able to reach 127.0.0.1:$MAIL_PORT, i.e. run on this machine.
//   BASE=http://localhost:8080 node reset.mjs
import net from "node:net";
import { BASE, ok, start, scanThemes, signIn, noHorizontalScroll } from "./helpers.mjs";

const PORT = Number(process.env.MAIL_PORT || 2525);
const mails = [];
const sink = net.createServer((c) => {
  let data = false, msg = "", buf = "";
  const w = (s) => c.write(s + "\r\n");
  w("220 sink");
  c.on("data", (d) => {
    buf += d.toString();
    let i;
    while ((i = buf.indexOf("\r\n")) >= 0) {
      const l = buf.slice(0, i); buf = buf.slice(i + 2);
      if (data) { if (l === ".") { data = false; mails.push(msg); msg = ""; w("250 ok"); } else msg += l + "\n"; continue; }
      if (l.startsWith("EHLO")) w("250 sink"); else if (l === "DATA") { data = true; w("354 go"); } else if (l === "QUIT") { w("221 bye"); c.end(); } else w("250 ok");
    }
  });
});
await new Promise((r) => sink.listen(PORT, "127.0.0.1", r));

const R = Math.floor(Math.random() * 1e6), U = `rb${R}`, P1 = `first-long-pass-${R}`, P2 = `second-long-pass-${R}`;
const dn = `uid=${U},ou=people,dc=example,dc=org`;
const { page: p, ctx, browser, finish, consoleErrors } = await start();
const api = (method, path, body) => p.evaluate(async ({ method, path, body }) => {
  const r = await fetch(path, { method, headers: { "Content-Type": "application/json", "X-Requested-With": "dapper" }, body: body ? JSON.stringify(body) : undefined });
  return r.status;
}, { method, path, body });
const waitMail = async (n) => { for (let i = 0; i < 60 && mails.length < n; i++) await new Promise((r) => setTimeout(r, 250)); return mails.length >= n; };

try {
  await p.goto(BASE + "/");
  await signIn(p, "alice", "alicepass");
  ok("email is turned on", (await api("PUT", "/api/admin/mail", { enabled: true, host: "127.0.0.1", port: PORT, security: "none", from: "dapper@example.org", publicURL: BASE })) === 200);
  ok("a person with an email address is created", (await api("POST", "/api/admin/users", { username: U, attrs: { givenName: ["Reset"], sn: ["Browser"], mail: [`${U}@example.org`] }, password: P1 })) === 200);
  await p.getByRole("button", { name: "Sign out" }).click();

  // the sign-in page offers the link
  await p.getByLabel("Username or email").waitFor();
  const link = p.getByRole("link", { name: "Forgot your password?" });
  await link.waitFor();
  ok("the sign-in page offers 'Forgot your password?'", true);
  await link.click();
  await p.getByRole("heading", { name: "Forgot your password?" }).waitFor();
  await scanThemes(p, "forgot password page");
  ok("the button waits for a name", await p.getByRole("button", { name: "Email me a link" }).isDisabled());
  await p.getByLabel("Username or email").fill(U);
  await p.getByRole("button", { name: "Email me a link" }).click();
  await p.getByText(/is on its way/).waitFor();
  ok("the page answers without saying whether the account exists", true);
  ok("the email arrives", await waitMail(1));
  const url = (mails.at(-1).match(/https?:\/\/\S+#\/reset\/[A-Za-z0-9_-]+/) || [])[0];
  ok("the email holds a reset link", !!url, mails.at(-1));

  // the link
  await p.goto(url);
  await p.getByRole("heading", { name: "Choose a new password" }).waitFor();
  await p.getByLabel("New password").waitFor();
  await scanThemes(p, "choose a new password page");
  await p.getByLabel("New password").fill("short");
  await p.getByLabel("Type it again").fill("short");
  ok("a too-short password can't be saved", await p.getByRole("button", { name: "Save new password" }).isDisabled());
  await p.getByLabel("New password").fill(P2);
  await p.getByLabel("Type it again").fill(P2 + "x");
  await p.getByText("The two passwords don't match.").waitFor();
  ok("a mismatch is pointed out", await p.getByRole("button", { name: "Save new password" }).isDisabled());
  await p.setViewportSize({ width: 390, height: 800 });
  ok("no sideways scrolling on a phone", await noHorizontalScroll(p));
  await p.setViewportSize({ width: 1100, height: 900 });
  await p.getByLabel("Type it again").fill(P2);
  await p.getByRole("button", { name: "Save new password" }).click();
  await p.getByText(/Your password was changed/).waitFor();
  ok("the new password is saved", true);
  ok("a 'password changed' notice arrives", await waitMail(2) && /Your password was changed/.test(mails.at(-1)));

  // the same link again, and a made-up one
  await p.goto(url); await p.reload();
  await p.getByText(/expired or was already used/).waitFor();
  ok("the used link is explained, with a way to get another", await p.getByRole("link", { name: "Ask for a new link" }).isVisible());

  // and it all works
  await p.goto(BASE + "/#/");
  await signIn(p, U, P2);
  ok("the person signs in with the new password", await p.getByRole("button", { name: "Sign out" }).isVisible());
  // a welcome message from the New person page
  await p.getByRole("button", { name: "Sign out" }).click();
  await signIn(p, "alice", "alicepass");
  await p.goto(BASE + "/#/people/new");
  const WN = `wb${R}`, WP = `welcome-long-pass-${R}`;
  await p.getByLabel("First name").fill("Wel"); await p.getByLabel("Last name").fill("Come");
  const box = p.getByLabel(/Email them a welcome message/);
  await box.waitFor();
  ok("the welcome box is offered but waits for an email address", await box.isDisabled());
  await p.getByLabel("Email", { exact: true }).fill(`${WN}@example.org`);
  ok("it is on once there is an address", await box.isEnabled() && await box.isChecked());
  await p.getByLabel("Username *").fill(WN);
  const n0 = mails.length;
  await p.getByRole("button", { name: "Create account" }).click();
  await p.getByText(/A welcome message with a link to choose a password was sent/).waitFor();
  ok("the page says the welcome message was sent", true);
  ok("the welcome email arrives", await waitMail(n0 + 1));
  const wurl = (mails.filter((m) => /Welcome to/.test(m)).at(-1)?.match(/https?:\/\/\S+#\/reset\/[A-Za-z0-9_-]+/) || [])[0];
  ok("it holds a link", !!wurl);
  await p.getByRole("button", { name: "Sign out" }).click();
  await p.goto(wurl);
  await p.getByLabel("New password").fill(WP); await p.getByLabel("Type it again").fill(WP);
  await p.getByRole("button", { name: "Save new password" }).click();
  await p.getByText(/Your password was changed/).waitFor();
  await p.goto(BASE + "/#/");
  await signIn(p, WN, WP);
  ok("the new person signs in with the password they chose", await p.getByRole("button", { name: "Sign out" }).isVisible());
  await p.getByRole("button", { name: "Sign out" }).click();
  await signIn(p, "alice", "alicepass");
  await api("DELETE", `/api/admin/user?dn=${encodeURIComponent(`uid=${WN},ou=people,dc=example,dc=org`)}`);
  const other = consoleErrors.filter((e) => !e.includes("status of 400") && !e.includes("status of 401"));
  ok("no errors in the browser console", other.length === 0, other);
} catch (e) {
  ok("the test ran to the end", false, String(e).split("\n")[0]);
}
try {
  await p.goto(BASE + "/"); await p.evaluate(() => fetch("/api/logout", { method: "POST", headers: { "X-Requested-With": "dapper" } }));
  await p.reload(); await signIn(p, "alice", "alicepass");
  await api("DELETE", `/api/admin/user?dn=${encodeURIComponent(dn)}`);
  await api("PUT", "/api/admin/mail", { enabled: false, port: 587, security: "starttls" });
} catch { /* clean-up is best effort */ }
sink.close();
await finish();
