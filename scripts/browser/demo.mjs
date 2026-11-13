// Browser test against the demo directory (docker-compose.demo.yml, configured: sign in as alice / alicepass):
// pictures on the People page, and what happens when a session ends.
//   BASE=http://localhost:8080 node demo.mjs
import { BASE, ok, start, scanThemes, signIn, noHorizontalScroll } from "./helpers.mjs";

const { page: p, ctx, finish, consoleErrors } = await start();
const dn = (u) => `uid=${u},ou=people,dc=example,dc=org`;
const WITH = ["ada.reyes0", "ben.okafor1"];

await p.goto(BASE + "/");
await p.getByLabel("Username or email").waitFor();
await signIn(p, "alice", "alicepass");
consoleErrors.length = 0;

// give two people a (large) picture, made in the page so the test needs no image files
const put = (user) => p.evaluate(async ({ url }) => {
  const c = document.createElement("canvas"); c.width = 400; c.height = 300;
  const g = c.getContext("2d"); g.fillStyle = "#3b6ea5"; g.fillRect(0, 0, 400, 300); g.fillStyle = "#f2d3a3"; g.beginPath(); g.arc(200, 130, 70, 0, 7); g.fill();
  const blob = await new Promise((r) => c.toBlob(r, "image/jpeg", 0.9));
  return (await fetch(url, { method: "PUT", headers: { "Content-Type": "image/jpeg", "X-Requested-With": "dapper" }, body: blob })).status;
}, { url: `/api/admin/user/photo?dn=${encodeURIComponent(dn(user))}` });
const del = (user) => p.evaluate(async (url) => (await fetch(url, { method: "DELETE", headers: { "X-Requested-With": "dapper" } })).status,
  `/api/admin/user/photo?dn=${encodeURIComponent(dn(user))}`);

try {
  for (const u of WITH) ok(`a picture is set for ${u}`, (await put(u)) === 200);
  let thumbs = 0, full = 0;
  p.on("request", (r) => { const u = r.url(); if (u.includes("/api/photo")) (u.includes("size=thumb") ? thumbs++ : full++); });

  await p.goto(BASE + "/#/people");
  await p.reload(); // the list was loaded before the pictures existed
  await p.getByRole("link", { name: "Ada Reyes" }).first().waitFor();
  await p.waitForTimeout(800);
  const imgs = p.locator("table.users tbody img.avatar");
  ok("only the people with pictures show one", (await imgs.count()) === 2);
  ok("the pictures are small thumbnails", await imgs.first().evaluate((i) => i.complete && i.naturalWidth > 0 && i.naturalWidth <= 72));
  ok("the list asks for thumbnails only, one per picture", full === 0 && thumbs === 2, { full, thumbs });
  ok("pictures load lazily and have empty alt text", await imgs.first().evaluate((i) => i.loading === "lazy" && i.alt === ""));
  ok("everyone else gets an initials circle", (await p.locator("table.users tbody .avatar.blank").count()) > 5);
  const lefts = await p.locator("table.users tbody .namecell a.rowlink").evaluateAll((as) => [...new Set(as.map((a) => Math.round(a.getBoundingClientRect().left)))]);
  ok("all names line up whether or not there is a picture", lefts.length === 1, lefts);
  await scanThemes(p, "People page with pictures");
  await p.setViewportSize({ width: 390, height: 800 });
  ok("the People page fits a phone", await noHorizontalScroll(p));
  await p.setViewportSize({ width: 1100, height: 900 });
} finally {
  for (const u of WITH) await del(u);
}
ok("no console errors on the People page", consoleErrors.length === 0, consoleErrors);

// a server Dapper doesn't run: the Server page says where backups come from instead of leaving a gap
await p.goto(BASE + "/#/server");
await p.getByText("Backups", { exact: true }).waitFor();
ok("the Server page explains that backups are for a Dapper-run server", await p.getByText(/only for a directory server that/).isVisible());
ok("and offers no Back up now button", (await p.getByRole("button", { name: "Back up now" }).count()) === 0);
await scanThemes(p, "Server page (server not run by Dapper)");

// service accounts: create one, see why it can't read yet, let it read, reset its password, delete it
const SUF = "dc=example,dc=org", BOX = `ou=services,${SUF}`, SVC = `cn=browsersvc,${BOX}`;
const api = (method, path, body) => p.evaluate(async ({ method, path, body }) => {
  const r = await fetch(path, { method, headers: { "X-Requested-With": "dapper", "Content-Type": "application/json" }, body: body ? JSON.stringify(body) : undefined });
  const t = await r.text(); let j = null; try { j = JSON.parse(t); } catch { /* not JSON */ }
  return { status: r.status, body: j };
}, { method, path, body });
await api("POST", "/api/admin/overlays/unlock", { dn: "cn=admin,cn=config", password: "config" });
const savedAcl = (await api("GET", "/api/admin/server/config")).body.acl;
await api("POST", "/api/admin/overlays/lock", {}); // start locked, as a normal visit would
try {
  await p.goto(BASE + "/#/service-accounts");
  await p.getByRole("heading", { name: "Service accounts" }).waitFor();
  ok("the Service accounts tab is offered", await p.getByRole("link", { name: "Service accounts" }).isVisible());
  await scanThemes(p, "Service accounts page");
  await p.getByRole("button", { name: "+ New service account" }).click();
  const dlg = p.getByRole("dialog", { name: "New service account" });
  await dlg.getByLabel(/^Name/).fill("browsersvc");
  await dlg.getByLabel(/What it's used for/).fill("browser test");
  ok("creating needs approval", await dlg.getByRole("button", { name: "Create" }).isDisabled());
  ok("the plan names what will be created", await dlg.getByText(SVC).isVisible());
  await scanThemes(p, "New service account dialog");
  await dlg.getByLabel(/Yes, create it/).check();
  await dlg.getByRole("button", { name: "Create" }).click();
  const res = p.getByRole("dialog", { name: "Service account ready" });
  await res.waitFor();
  ok("the password is shown once", (await res.locator(".secret code").count()) === 1 && (await res.locator(".secret code").innerText()).length === 24);
  ok("it explains the account can't read yet", await res.getByText(/can't see any people yet/).isVisible());
  ok("and gives the settings for the application", await res.getByText(SVC).first().isVisible());
  ok("with the unlock step when the server settings are locked", await res.getByText(/server settings are unlocked/).isVisible());
  await api("POST", "/api/admin/overlays/unlock", { dn: "cn=admin,cn=config", password: "config" });
  await res.getByRole("button", { name: "Check again" }).click();
  await res.getByRole("button", { name: "Show me the change" }).click();
  await res.getByText(/Let service accounts/).first().waitFor();
  ok("the access rule change is explained in plain words", true);
  await scanThemes(p, "Service account result dialog");
  await res.getByLabel(/Yes, make this change/).check();
  await res.getByRole("button", { name: "Let service accounts read" }).click();
  await res.getByText(/can read people .* and cannot see password hashes/).waitFor({ timeout: 20000 });
  ok("after the rule, it reads people but not password hashes", true);
  await res.getByRole("button", { name: "Done" }).click();
  await p.getByRole("row", { name: /browsersvc/ }).waitFor();
  ok("the account is listed", true);

  await p.getByRole("row", { name: /browsersvc/ }).getByRole("button", { name: "Reset password" }).click();
  const rst = p.getByRole("dialog", { name: /Reset the password of browsersvc/ });
  ok("reset needs approval", await rst.getByRole("button", { name: "Reset password" }).isDisabled());
  await rst.getByLabel(/Yes, set a new password/).check();
  await rst.getByRole("button", { name: "Reset password" }).click();
  await p.getByRole("dialog", { name: "Service account ready" }).waitFor();
  ok("a new password is shown", (await p.locator(".secret code").count()) === 1);
  await p.getByRole("button", { name: "Done" }).click();

  await p.setViewportSize({ width: 390, height: 800 });
  ok("the page fits a phone", await noHorizontalScroll(p));
  await p.setViewportSize({ width: 1100, height: 900 });
  p.once("dialog", (d) => d.accept());   // "Delete the service account…?"
  await p.getByRole("row", { name: /browsersvc/ }).getByRole("button", { name: "Delete" }).click();
  await p.getByText("No service accounts yet.").waitFor();
  ok("deleting removes it", true);
} finally {
  await api("DELETE", "/api/admin/service-account?dn=" + encodeURIComponent(SVC)); // in case the test stopped early
  await api("POST", "/api/admin/server/apply", { kind: "acl.set", rules: savedAcl, confirmed: true });
  await api("DELETE", "/api/admin/entry?dn=" + encodeURIComponent(BOX));
  await api("POST", "/api/admin/overlays/lock", {});
}
ok("the demo's access rules are back as they were", JSON.stringify((await (async () => { await api("POST", "/api/admin/overlays/unlock", { dn: "cn=admin,cn=config", password: "config" }); const c = (await api("GET", "/api/admin/server/config")).body.acl; await api("POST", "/api/admin/overlays/lock", {}); return c; })())) === JSON.stringify(savedAcl));
await p.goto(BASE + "/#/people");

// a session that ends behind the page's back
await p.goto(BASE + "/#/groups");
await p.getByRole("heading", { name: "Groups" }).first().waitFor();
await ctx.clearCookies();
await p.getByRole("link", { name: "People" }).first().click();
await p.getByText("Your session has ended. Please sign in again.").waitFor({ timeout: 10000 });
ok("an ended session returns to the sign-in screen with a note", await p.getByLabel("Username or email").isVisible());
ok("and keeps the page they were on", p.url().endsWith("#/people"));
await signIn(p, "alice", "alicepass");
ok("signing in again returns to that page", p.url().endsWith("#/people"));
await p.getByRole("button", { name: "Sign out" }).click();
await p.getByLabel("Username or email").waitFor();
ok("signing out shows no 'session ended' note", (await p.getByText("Your session has ended").count()) === 0);
await p.getByLabel("Username or email").fill("alice"); await p.getByLabel("Password").fill("wrong");
await p.getByRole("button", { name: "Sign in" }).click();
await p.getByText("Incorrect username or password.").waitFor();
ok("a wrong password shows its own message, not the session note", (await p.getByText("Your session has ended").count()) === 0);
await finish();
