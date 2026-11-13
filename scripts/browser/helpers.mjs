import { chromium } from "playwright";
import AxeBuilder from "@axe-core/playwright";

export const BASE = process.env.BASE || "http://localhost:8080";
export const THEMES = ["Light", "Dark", "Sepia"];

let failed = 0, passed = 0;
export function ok(name, cond, detail) {
  if (cond) { passed++; console.log("ok  ", name); }
  else { failed++; console.log("FAIL", name, detail === undefined ? "" : JSON.stringify(detail)); }
}

/** Opens a browser and returns what a test needs; call finish() at the end for the exit code. */
export async function start(viewport = { width: 1100, height: 900 }) {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport });
  const page = await ctx.newPage();
  const consoleErrors = [];
  page.on("console", (m) => { if (m.type() === "error") consoleErrors.push(m.text()); });
  page.on("pageerror", (e) => consoleErrors.push(String(e)));
  return {
    browser, ctx, page, consoleErrors,
    finish: async () => {
      await browser.close();
      console.log(`\npassed ${passed}, failed ${failed}`);
      process.exit(failed === 0 ? 0 : 1);
    },
  };
}

export async function setTheme(page, label) {
  await page.locator(".corner select, header select").first().selectOption({ label });
}

/** Scans the page for serious or critical accessibility problems in each theme (then puts the theme back). */
export async function scanThemes(page, where) {
  for (const t of THEMES) {
    await setTheme(page, t);
    const r = await new AxeBuilder({ page }).analyze();
    const bad = r.violations.filter((v) => v.impact === "serious" || v.impact === "critical");
    ok(`${where}: no serious accessibility problems in the ${t} theme`, bad.length === 0,
      bad.map((v) => `${v.id}: ${v.nodes.slice(0, 2).map((n) => n.target.join(" ")).join(" | ")}`));
  }
  await setTheme(page, "Light");
}

export async function signIn(page, user, password) {
  await page.getByLabel("Username or email").fill(user);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.getByRole("button", { name: "Sign out" }).waitFor();
}

export const noHorizontalScroll = (page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth);
