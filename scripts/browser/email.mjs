// Browser test of the Email page against the demo directory (sign in as alice / alicepass).
//   BASE=http://localhost:8080 node email.mjs
// With SMTP_SINK=host:port set (a mail server that accepts anything, without encryption, on this machine) it also
// sends a real test message.
import { BASE, ok, start, scanThemes, signIn, noHorizontalScroll } from "./helpers.mjs";

const SINK = process.env.SMTP_SINK;
const { page: p, finish, consoleErrors } = await start();
await p.goto(BASE + "/");
await signIn(p, "alice", "alicepass");
// start from "never set up", so the test can be run again and again
await p.evaluate(() => fetch("/api/admin/mail", { method: "PUT", headers: { "Content-Type": "application/json", "X-Requested-With": "dapper" },
  body: JSON.stringify({ enabled: false, host: "", port: 587, security: "starttls", username: "", from: "", fromName: "", publicURL: "" }) }));
consoleErrors.length = 0;

try {
  await p.getByRole("link", { name: "Email" }).click();
  await p.getByRole("heading", { name: "Email" }).waitFor();
  ok("the Email tab opens", true);
  ok("email starts off", !(await p.getByLabel("Send email", { exact: true }).isChecked()));
  await scanThemes(p, "email page");

  // choosing a connection type moves the port to match
  await p.locator(".page select").selectOption("tls");
  ok("choosing TLS sets port 465", (await p.getByLabel("Port", { exact: true }).inputValue()) === "465");
  await p.locator(".page select").selectOption("starttls");
  ok("choosing STARTTLS sets port 587", (await p.getByLabel("Port", { exact: true }).inputValue()) === "587");

  // turning it on with an incomplete form is refused, in plain words
  await p.getByLabel("Send email", { exact: true }).check();
  await p.getByRole("button", { name: "Save" }).click();
  await p.getByText(/Enter the mail server/i).waitFor();
  ok("incomplete settings are refused with a plain message", true);

  // an unencrypted connection to another machine is refused
  await p.getByLabel("Mail server", { exact: true }).fill("mail.example.org");
  await p.getByLabel("Send from (address)").fill("dapper@example.org");
  await p.getByLabel("Dapper's web address").fill("https://dapper.example.org");
  await p.locator(".page select").selectOption("none");
  await p.getByRole("button", { name: "Save" }).click();
  await p.getByText(/only allowed to a mail server on this machine/i).waitFor();
  ok("no encryption is refused for a remote server", true);

  // a test message to a server that isn't there fails clearly
  await p.getByLabel("Mail server", { exact: true }).fill("127.0.0.1");
  await p.getByLabel("Port", { exact: true }).fill("1");
  await p.getByLabel("Send to").fill("ann@example.org");
  await p.getByRole("button", { name: "Send test message" }).click();
  await p.getByText(/Couldn't reach the mail server/).waitFor();
  ok("an unreachable server is explained", true);

  if (SINK) {
    const [host, port] = SINK.split(":");
    await p.getByLabel("Mail server", { exact: true }).fill(host);
    await p.getByLabel("Port", { exact: true }).fill(port);
    await p.getByRole("button", { name: "Send test message" }).click();
    await p.getByText(/A test message was sent to ann@example.org/).waitFor();
    ok("a test message is sent", true);
    await p.getByRole("button", { name: "Save" }).click();
    await p.getByText("Saved. Dapper can now send email.").waitFor();
    await p.reload();
    await p.getByRole("heading", { name: "Email" }).waitFor();
    ok("saved settings come back after a reload", (await p.getByLabel("Mail server", { exact: true }).inputValue()) === host && await p.getByLabel("Send email", { exact: true }).isChecked());
    ok("the page never shows a password", (await p.locator("input[type=password]").inputValue()) === "");
    await p.getByLabel("Send email", { exact: true }).uncheck();
    await p.getByRole("button", { name: "Save" }).click();
    await p.getByText("Saved. Email is turned off.").waitFor();
  }

  await p.setViewportSize({ width: 390, height: 800 });
  ok("no sideways scrolling on a phone", await noHorizontalScroll(p));
  // the refusals above are deliberate, and the browser logs each 400 answer
  const other = consoleErrors.filter((e) => !e.includes("status of 400"));
  ok("no errors in the browser console", other.length === 0, other);
} catch (e) {
  ok("the test ran to the end", false, String(e).split("\n")[0]);
}
await finish();
