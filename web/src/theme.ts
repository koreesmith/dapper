export const THEMES = ["light", "dark", "sepia"] as const;
export type Theme = (typeof THEMES)[number];

export function currentTheme(): Theme {
  const t = document.documentElement.getAttribute("data-theme");
  return (THEMES as readonly string[]).includes(t ?? "") ? (t as Theme) : "light";
}

export function setTheme(t: Theme) {
  document.documentElement.setAttribute("data-theme", t);
  try { localStorage.setItem("dapper-theme", t); } catch { /* storage unavailable: choice lasts this visit only */ }
}
