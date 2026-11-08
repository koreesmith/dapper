import { useEffect, useState } from "react";

const read = () => decodeURIComponent(window.location.hash.replace(/^#/, "")) || "/";

/** Tiny hash router: the path lives in the URL so links, refresh and Back all work. */
export function useRoute(): string {
  const [path, setPath] = useState(read());
  useEffect(() => {
    const on = () => setPath(read());
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return path;
}

export const href = (...parts: string[]) => "#" + parts.map((p, i) => (i === 0 ? p : encodeURIComponent(p))).join("/");
export const go = (path: string) => { window.location.hash = path; };
