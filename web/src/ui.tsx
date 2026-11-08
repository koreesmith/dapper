import { ReactNode, useEffect, useId, useRef, useState } from "react";
import { Attrs, Field, FieldValue } from "./api";

/** Modal dialog with focus on open, Esc to close. */
export function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    ref.current?.focus();
    const on = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, [onClose]);
  return (
    <div className="backdrop" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose(); }}>
      <div className="modal" role="dialog" aria-modal="true" aria-label={title} tabIndex={-1} ref={ref}>
        <h3>{title}</h3>
        {children}
      </div>
    </div>
  );
}

/** Shows a generated password once, with a copy button. */
export function SecretBox({ value, note }: { value: string; note?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="secret">
      <code>{value}</code>
      <button type="button" onClick={async () => {
        try { await navigator.clipboard.writeText(value); setCopied(true); setTimeout(() => setCopied(false), 1500); } catch { /* clipboard blocked */ }
      }}>{copied ? "Copied" : "Copy"}</button>
      {note && <p className="sub">{note}</p>}
    </div>
  );
}

export function Notice({ kind, children }: { kind: "error" | "ok"; children: ReactNode }) {
  return <p className={kind === "error" ? "error" : "okmsg"} role={kind === "error" ? "alert" : "status"}>{children}</p>;
}

/** Rough strength indicator; the server's policy is what actually decides. */
export function strength(pw: string): { score: number; label: string } {
  let s = 0;
  if (pw.length >= 8) s++;
  if (pw.length >= 12) s++;
  if (/[a-z]/.test(pw) && /[A-Z]/.test(pw)) s++;
  if (/\d/.test(pw) && /[^A-Za-z0-9]/.test(pw)) s++;
  return { score: s, label: ["Too weak", "Weak", "Okay", "Good", "Strong"][s] };
}

export function StrengthMeter({ password }: { password: string }) {
  const { score, label } = strength(password);
  if (!password) return null;
  return (
    <div className="meter" aria-live="polite">
      <div className={`bar s${score}`} style={{ width: `${(score / 4) * 100}%` }} />
      <span className="sub">{label}</span>
    </div>
  );
}

// ---- field forms ----

export type FormState = Record<string, string[]>;

export function initialValues(fields: FieldValue[]): FormState {
  const s: FormState = {};
  for (const f of fields) s[f.attr] = f.values.length ? [...f.values] : [""];
  return s;
}

const clean = (v: string[]) => v.map((x) => x.trim()).filter(Boolean);

/** Attributes whose values differ from the originals. */
export function changedAttrs(fields: FieldValue[], state: FormState): Attrs {
  const out: Attrs = {};
  for (const f of fields) {
    const a = clean(f.values), b = clean(state[f.attr] ?? []);
    if (a.length !== b.length || a.some((x, i) => x !== b[i])) out[f.attr] = b;
  }
  return out;
}

export function FieldEditor({ field, values, onChange, readOnly }: {
  field: Field; values: string[]; onChange: (v: string[]) => void; readOnly?: boolean;
}) {
  const id = useId();
  const vals = values.length ? values : [""];
  const inputType = field.type === "textarea" ? undefined : field.type;
  const set = (i: number, v: string) => onChange(vals.map((x, j) => (j === i ? v : x)));

  if (readOnly) {
    const shown = clean(vals);
    return (
      <div className="field">
        <span className="flabel">{field.label}</span>
        <span className="fvalue">{shown.length ? shown.map((v) => <div key={v}>{v}</div>) : <span className="sub">Not set</span>}</span>
      </div>
    );
  }
  return (
    <div className="field">
      <label className="flabel" htmlFor={`${id}-0`}>{field.label}{field.required && <span aria-hidden="true"> *</span>}</label>
      <div className="finputs">
        {vals.map((v, i) => (
          <div className="frow" key={i}>
            {field.type === "textarea"
              ? <textarea id={`${id}-${i}`} rows={3} value={v} onChange={(e) => set(i, e.target.value)} />
              : <input id={`${id}-${i}`} type={inputType} value={v} required={field.required} onChange={(e) => set(i, e.target.value)}
                  autoComplete="off" />}
            {field.multi && vals.length > 1 && (
              <button type="button" className="plain" aria-label={`Remove ${field.label} ${i + 1}`}
                onClick={() => onChange(vals.filter((_, j) => j !== i))}>✕</button>
            )}
          </div>
        ))}
        {field.multi && (
          <button type="button" className="link" onClick={() => onChange([...vals, ""])}>+ Add another {field.label.toLowerCase()}</button>
        )}
      </div>
    </div>
  );
}
