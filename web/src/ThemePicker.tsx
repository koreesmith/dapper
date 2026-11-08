import { useState } from "react";
import { currentTheme, setTheme, Theme } from "./theme";

const LABELS: Record<Theme, string> = { light: "Light", dark: "Dark", sepia: "Sepia" };

export function ThemePicker() {
  const [theme, set] = useState<Theme>(currentTheme());
  return (
    <label className="theme-picker">
      <span className="sr-only">Theme</span>
      <select value={theme} onChange={(e) => { const t = e.target.value as Theme; setTheme(t); set(t); }}>
        {(Object.keys(LABELS) as Theme[]).map((t) => <option key={t} value={t}>{LABELS[t]}</option>)}
      </select>
    </label>
  );
}
