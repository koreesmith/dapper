import { useRef, useState } from "react";
import { Notice } from "./ui";

/** Shrinks a chosen picture to at most 512 pixels and re-saves it as a JPEG, so uploads stay small. */
export async function prepareJpeg(file: File): Promise<Blob> {
  if (!file.type.startsWith("image/")) throw new Error("Choose a picture file (JPEG, PNG or similar).");
  const bmp = await createImageBitmap(file).catch(() => { throw new Error("That picture couldn't be read. Try a different file."); });
  const scale = Math.min(1, 512 / Math.max(bmp.width, bmp.height));
  const c = document.createElement("canvas");
  c.width = Math.max(1, Math.round(bmp.width * scale));
  c.height = Math.max(1, Math.round(bmp.height * scale));
  const ctx = c.getContext("2d");
  if (!ctx) throw new Error("This browser can't prepare pictures.");
  ctx.fillStyle = "#fff"; // JPEG has no transparency
  ctx.fillRect(0, 0, c.width, c.height);
  ctx.drawImage(bmp, 0, 0, c.width, c.height);
  return new Promise((res, rej) => c.toBlob((b) => (b ? res(b) : rej(new Error("The picture couldn't be prepared."))), "image/jpeg", 0.88));
}

export function PhotoPanel({ name, src, has, canChange, onSet, onRemove, onChanged }: {
  name: string; src: string; has: boolean; canChange: boolean;
  onSet: (b: Blob) => Promise<void>; onRemove: () => Promise<unknown>; onChanged: () => void;
}) {
  const input = useRef<HTMLInputElement>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const initials = name.split(/\s+/).filter(Boolean).slice(0, 2).map((w) => w[0]!.toUpperCase()).join("");

  const run = async (f: () => Promise<unknown>) => {
    setBusy(true); setErr("");
    try { await f(); onChanged(); } catch (x) { setErr((x as Error).message); } finally { setBusy(false); }
  };

  return (
    <div className="panel photo-panel">
      <h3>Picture</h3>
      <div className="photo-row">
        {has ? <img className="avatar" src={src} alt={`Picture of ${name}`} width={96} height={96} /> : <div className="avatar blank" aria-hidden="true">{initials || "?"}</div>}
        {canChange ? (
          <div>
            <input ref={input} type="file" accept="image/*" hidden aria-label="Choose a picture" onChange={(e) => {
              const f = e.target.files?.[0]; e.target.value = "";
              if (f) run(async () => onSet(await prepareJpeg(f)));
            }} />
            <button type="button" disabled={busy} onClick={() => input.current?.click()}>{has ? "Change picture\u2026" : "Upload a picture\u2026"}</button>{" "}
            {has && <button type="button" disabled={busy} onClick={() => run(onRemove)}>Remove</button>}
            <p className="sub">Any common picture works; it's shrunk before it's saved.</p>
          </div>
        ) : <p className="sub">{has ? "Only an administrator can change this picture." : "No picture. Only an administrator can add one."}</p>}
      </div>
      {err && <Notice kind="error">{err}</Notice>}
    </div>
  );
}
