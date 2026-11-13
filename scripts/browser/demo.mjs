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
