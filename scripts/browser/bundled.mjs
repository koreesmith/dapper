// Browser test of a Dapper-run directory server, from an UNCONFIGURED Dapper started with
// docker-compose.server.yml (LDAP_HOSTNAME=ldap.test): the setup wizard, then the pages that follow it.
//   SETUP_CODE=<code> BASE=http://localhost:8080 node bundled.mjs
import { BASE, ok, start, scanThemes, signIn, noHorizontalScroll } from "./helpers.mjs";

const CODE = process.env.SETUP_CODE;
if (!CODE) { console.error("Set SETUP_CODE to the code from Dapper's log."); process.exit(2); }
const HOST = process.env.EXPECT_HOST || "ldap.test";
const { page: p, ctx, finish, consoleErrors } = await start();

await p.goto(BASE + "/");
await p.getByLabel("Setup code").fill("wrong-code"); await p.getByRole("button", { name: "Continue" }).click();
await p.getByText("That setup code isn't right").waitFor();
ok("a wrong setup code is refused", true);
await p.getByLabel("Setup code").fill(CODE); await p.getByRole("button", { name: "Continue" }).click();

await p.getByRole("heading", { name: "Do you already have an LDAP server?" }).waitFor();
await scanThemes(p, "setup: choose a server");
await p.getByRole("button", { name: /I already have an LDAP server/ }).click();
await p.getByRole("heading", { name: "Your directory" }).waitFor();
await p.getByRole("button", { name: "Back" }).click();
await p.getByRole("heading", { name: "Do you already have an LDAP server?" }).waitFor();
ok("the existing-server path opens, and Back returns to the choice", true);

await p.getByRole("button", { name: /I need a new one/ }).click();
await p.getByRole("button", { name: "Look for the server" }).click();
await p.getByText("Found the new server and connected to it").waitFor();
ok("the new server is found", true);
await scanThemes(p, "setup: your server");
await p.getByRole("button", { name: "Next" }).click();

await p.getByLabel("Organization name").fill("Test Org");
await p.getByLabel("Domain name").fill("test.lan");
ok("the directory name is shown as you type", await p.getByText("dc=test,dc=lan").first().isVisible());
await scanThemes(p, "setup: your directory");
await p.getByRole("button", { name: "Next" }).click();
await p.getByRole("heading", { name: "Linux accounts" }).waitFor();
await scanThemes(p, "setup: Linux accounts");
await p.getByRole("button", { name: "Next" }).click();

await p.getByLabel("Username").fill("alice"); await p.getByLabel("First name").fill("Alice"); await p.getByLabel("Last name").fill("Admin");
await p.locator("input[type=password]").fill("correct horse battery");
await scanThemes(p, "setup: administrator");
await p.getByRole("button", { name: "Review" }).click();
await p.getByText("Dapper will make these changes on the new server").waitFor();
const finishBtn = p.getByRole("button", { name: "Set up the server" });
ok("setup can't be finished before approving", await finishBtn.isDisabled());
await scanThemes(p, "setup: review");
await p.getByLabel(/Yes, set up/).check();
await finishBtn.click();
await p.getByRole("heading", { name: "Your directory server is ready" }).waitFor({ timeout: 90000 });
ok("two passwords are shown once", (await p.locator(".secret code").count()) === 2);
ok("the sssd settings name the server", await p.getByText(new RegExp(`ldap_uri = ldaps://${HOST}`)).isVisible());
await scanThemes(p, "setup: ready");
ok("the setup screens fit a phone", await (async () => { await p.setViewportSize({ width: 390, height: 800 }); const r = await noHorizontalScroll(p); await p.setViewportSize({ width: 1100, height: 900 }); return r; })());

await p.getByRole("button", { name: "Go to sign in" }).click();
await signIn(p, "alice", "correct horse battery");
ok("the new administrator signs in", true);
consoleErrors.length = 0;

// Server page: connection help and backups
await p.goto(BASE + "/#/server");
await p.getByText("Connect your machines").waitFor();
ok("the Server page shows how to connect machines", await p.getByText(new RegExp(`ldap_uri = ldaps://${HOST}`)).isVisible());
await p.getByText("Backups", { exact: true }).waitFor();
const rows = () => p.locator("#backups tbody tr").count();
const before = await rows();
ok("downloads are gated until the server settings are unlocked", before === 0 || await p.getByText("Unlock to download").first().isVisible());
await p.getByRole("button", { name: "Back up now" }).click();
for (let i = 0; i < 45 && (await rows()) <= before; i++) await p.waitForTimeout(1000);
ok("a new backup appears after Back up now", (await rows()) === before + 1);
await p.getByText("Restore from a backup").click();
ok("the restore steps show the exact commands", await p.getByText(/run --rm openldap restore dapper-ldap-manual-/).isVisible());
await p.getByText("I don't have this login").click();
ok("the unlock help for a Dapper-run server resets only the password, for config-admin",
  (await p.getByText(/replace: olcRootPW/).isVisible()) && (await p.getByText(/olcRootDN: cn=admin,cn=config/).count()) === 0 &&
  (await p.getByText(/cn=config-admin,cn=config/).count()) > 0);
await scanThemes(p, "Server page");

// Settings page: the trusted certificate
await p.goto(BASE + "/#/settings");
await p.getByRole("heading", { name: "Trusted certificate" }).waitFor();
ok("Settings shows the trusted certificate", await p.getByText(`CN=${HOST}`).isVisible());
await p.getByRole("button", { name: "Replace the certificate" }).click();
await p.getByLabel("Certificate (PEM text)").fill("nonsense");
await p.getByRole("button", { name: "Test and save" }).click();
await p.getByText("doesn't look like a certificate").waitFor();
ok("a bad certificate gets a plain error and changes nothing", true);
consoleErrors.length = 0; // that refusal is a 400 on purpose
await scanThemes(p, "Settings page");

// People page
await p.goto(BASE + "/#/people");
await p.getByRole("link", { name: "Alice Admin" }).waitFor();
await scanThemes(p, "People page");

ok("no console errors on the Settings and People pages", consoleErrors.length === 0, consoleErrors);

// A session that ends behind the page's back sends the person to sign in, and back again afterwards.
await p.goto(BASE + "/#/groups");
await p.getByRole("heading", { name: "Groups" }).first().waitFor();
await ctx.clearCookies();                                      // the session ends behind the page's back
await p.getByRole("link", { name: "People" }).first().click(); // and the person tries to do something
await p.getByText("Your session has ended. Please sign in again.").waitFor({ timeout: 10000 });
ok("an ended session returns to the sign-in screen with a note", await p.getByLabel("Username or email").isVisible());
ok("and keeps the page they were on", p.url().endsWith("#/people"));
await signIn(p, "alice", "correct horse battery");
ok("signing in again returns to that page", p.url().endsWith("#/people"));

for (const [path, name] of [["/#/server", "Server"], ["/#/settings", "Settings"]]) {
  await p.goto(BASE + path); await p.waitForTimeout(600);
  await p.setViewportSize({ width: 390, height: 800 });
  ok(`the ${name} page fits a phone`, await noHorizontalScroll(p));
  await p.setViewportSize({ width: 1100, height: 900 });
}
await finish();
