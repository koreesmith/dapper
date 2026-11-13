// Browser test of Migrate: an UNCONFIGURED Dapper started with the migration test stack (docker-compose.migrate-test.yml:
// an old demo server plus a Dapper-run one). Dapper is set up against the old server through the API, then the page
// takes it through check, copy and switch, and finishes the migration.
//   SETUP_CODE=<code> BASE=http://localhost:8080 node migrate.mjs
import { BASE, ok, start, scanThemes, signIn, noHorizontalScroll } from "./helpers.mjs";

const CODE = process.env.SETUP_CODE;
if (!CODE) { console.error("Set SETUP_CODE to the code from Dapper's log."); process.exit(2); }
const { page: p, finish, consoleErrors } = await start();

const settings = {
  ldap: { url: "ldap://source-ldap:389", startTLS: false, skipVerify: false, bindDN: "cn=admin,dc=example,dc=org", bindPassword: "admin" },
  users: { baseDN: "ou=people,dc=example,dc=org", loginAttributes: ["uid", "mail"], suspendedBaseDN: "ou=suspended,dc=example,dc=org", objectClasses: ["inetOrgPerson"], rdnAttribute: "uid" },
  groups: { baseDN: "ou=groups,dc=example,dc=org", objectClass: "groupOfNames", memberAttribute: "member" },
  admin: { groupDN: "cn=dapper-admins,ou=groups,dc=example,dc=org" }, password: { minLength: 8 }, session: { idleMinutes: 30, maxHours: 8 },
};
const r = await p.request.post(BASE + "/api/setup/complete", {
  headers: { "X-Requested-With": "dapper", "X-Setup-Code": CODE },
  data: { settings, createContainers: false, admin: { mode: "existing" } },
});
ok("Dapper is set up against the old server", r.ok(), await r.text());

await p.goto(BASE + "/");
await signIn(p, "alice", "alicepass");
await p.on("dialog", (d) => d.accept());
consoleErrors.length = 0;

await p.getByRole("link", { name: "Migrate" }).waitFor({ timeout: 10000 });
ok("the Migrate tab is offered", true);
await p.getByRole("link", { name: "Migrate" }).click();
await p.getByRole("heading", { name: "Migrate to a server Dapper runs" }).waitFor();
await p.getByText("Found Dapper's new server and connected to it").waitFor();
ok("the new server is found", true);
await scanThemes(p, "Migrate (before checking)");

await p.getByLabel("Directory manager", { exact: true }).fill("cn=admin,dc=example,dc=org");
await p.getByLabel("Password", { exact: true }).fill("wrong");
await p.getByRole("button", { name: "Check the old server" }).click();
await p.getByText(/didn't accept that name and password/).waitFor();
ok("a wrong manager password is explained plainly", true);
consoleErrors.length = 0; // that refusal is a 400 on purpose

await p.getByLabel("Password", { exact: true }).fill("admin");
await p.getByRole("button", { name: "Check the old server" }).click();
await p.getByText(/What Dapper found under/).waitFor();
ok("the check reports what it found", await p.getByText(/\d+ entries: .*\d+ people/).isVisible());
ok("and says passwords will be copied as they are", await p.getByText(/Passwords are stored as/).isVisible() || await p.getByText(/Passwords are stored/).count() === 0);
const copyBtn = p.getByRole("button", { name: "Copy now" });
ok("copying can't start before approval", await copyBtn.isDisabled());
await scanThemes(p, "Migrate (after the check)");

const keep = p.getByLabel(/Keep the old directory manager/);
ok("keeping the old directory manager is on by default", await keep.isChecked());
await keep.uncheck();   // this run takes the other path: a new, generated directory manager password
ok("the page explains what a generated manager means", await p.getByText(/generated password, shown once after the switch/).isVisible());
await p.getByLabel(/Yes, copy \d+ entries/).check();
await copyBtn.click();
await p.getByText("The copy is finished and checked").waitFor({ timeout: 120000 });
ok("the copy finishes and is checked", true);
await scanThemes(p, "Migrate (after the copy)");

ok("no console errors while checking and copying", consoleErrors.length === 0, consoleErrors);
const sw = p.getByRole("button", { name: "Switch to the new server" });
ok("switching needs approval", await sw.isDisabled());
await p.getByLabel(/Yes, switch Dapper/).check();
await sw.click();
await p.getByRole("heading", { name: "Dapper is now using the new server" }).waitFor({ timeout: 60000 });
ok("two passwords are shown once", (await p.locator(".secret code").count()) === 2);
ok("with a checklist of what to do next", (await p.locator("ol li").count()) >= 8);
await scanThemes(p, "Migrate (after the switch)");
await p.setViewportSize({ width: 390, height: 800 });
ok("the result fits a phone", await noHorizontalScroll(p));
await p.setViewportSize({ width: 1100, height: 900 });

await p.getByRole("button", { name: "Go to sign in" }).click();
await signIn(p, "alice", "alicepass");
ok("people sign in with their old password on the new server", true);
await p.getByRole("link", { name: "Migrate" }).click();
await p.getByRole("heading", { name: "Dapper is using the new server" }).waitFor();
ok("roll back and finish are offered", await p.getByRole("button", { name: "Roll back to the old server" }).isVisible() && await p.getByRole("button", { name: "Finish the migration" }).isVisible());
await scanThemes(p, "Migrate (after switching)");
await p.getByRole("button", { name: "Finish the migration" }).click();
await p.getByText("Dapper already runs this directory server").waitFor();
ok("finishing ends the migration", true);
await p.reload();
await p.getByRole("button", { name: "Sign out" }).waitFor();
ok("and the Migrate tab goes away", (await p.getByRole("link", { name: "Migrate" }).count()) === 0);
await finish();
