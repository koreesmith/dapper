import { useEffect, useState } from "react";
import { api, CAState, CertView } from "./api";
import { Notice } from "./ui";

function download(name: string, text: string) {
  const a = document.createElement("a");
  a.href = URL.createObjectURL(new Blob([text], { type: "application/x-pem-file" }));
  a.download = name; a.click();
  URL.revokeObjectURL(a.href);
}

/** How to point Linux servers and applications at a Dapper-run directory server. */
export function ConnectHelp({ host, suffix, certificate, cert, ldapPort = "389", ldapsPort = "636" }:
  { host: string; suffix: string; certificate?: string; cert?: CertView; ldapPort?: string; ldapsPort?: string }) {
  const sssd = `[domain/default]
id_provider = ldap
auth_provider = ldap
ldap_uri = ldaps://${host}${ldapsPort === "636" ? "" : ":" + ldapsPort}
ldap_search_base = ${suffix}
ldap_tls_cacert = /etc/pki/ca-trust/source/anchors/dapper-ldap.crt
ldap_tls_reqcert = demand`;
  return (
    <>
      <ul>
        <li>Address: <code>ldaps://{host}</code> (port {ldapsPort}), or <code>ldap://{host}</code> (port {ldapPort}) with StartTLS. Unencrypted connections are refused.</li>
        <li>Directory name (search base): <code>{suffix}</code></li>
      </ul>
      {certificate && (
        <>
          <p>The server uses its own certificate, so each machine has to trust it.{" "}
            <button type="button" onClick={() => download("dapper-ldap.crt", certificate)}>Download the certificate</button></p>
          {cert && <p className="sub">It is valid until {cert.notAfter}{cert.expired ? " (expired)" : cert.daysLeft < 60 ? ` (${cert.daysLeft} days left)` : ""} and names: {cert.names.join(", ")}.</p>}
          <p className="sub">On Red Hat, Rocky or AlmaLinux: copy it to <code>/etc/pki/ca-trust/source/anchors/dapper-ldap.crt</code> and run <code>sudo update-ca-trust</code>. On Debian or Ubuntu: copy it to <code>/usr/local/share/ca-certificates/dapper-ldap.crt</code> and run <code>sudo update-ca-certificates</code>.</p>
        </>
      )}
      <p>A starting point for <code>/etc/sssd/sssd.conf</code>:</p>
      <pre className="code">{sssd}</pre>
      <p className="sub">Anyone who can reach the server and connects over an encrypted connection can read the directory without signing in. You can tighten this under Server, in the access rules.</p>
    </>
  );
}

/** The certificate Dapper trusts for its directory connection (shown on Settings). */
export function TrustedCertificate() {
  const [ca, setCA] = useState<CAState | null>(null);
  const [pem, setPem] = useState("");
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: "ok" | "error"; text: string } | null>(null);
  useEffect(() => { api.ca().then(setCA).catch(() => setCA({ set: false })); }, []);
  if (!ca) return null;
  const c = ca.cert;
  return (
    <div className="panel" id="trusted-cert">
      <h3>Trusted certificate</h3>
      <p className="sub">Dapper checks the directory server's certificate against the ones your system trusts. If your server uses its own (self-signed) certificate, give Dapper that certificate here so it can connect safely without turning verification off.</p>
      {ca.error && <Notice kind="error">{ca.error}</Notice>}
      {!ca.set && <p>No extra certificate is set. Dapper uses the certificates your system trusts.</p>}
      {c && (
        <ul>
          <li>Certificate for: <strong>{c.subject}</strong></li>
          <li>Valid {c.notBefore} to {c.notAfter}{c.expired ? " (expired)" : c.daysLeft < 60 ? ` (${c.daysLeft} days left)` : ""}</li>
          {c.names.length > 0 && <li>Names: {c.names.join(", ")}</li>}
        </ul>
      )}
      {msg && <Notice kind={msg.kind}>{msg.text}</Notice>}
      {!open && <div className="formactions"><button type="button" onClick={() => { setOpen(true); setMsg(null); }}>{ca.set ? "Replace the certificate" : "Add a certificate"}</button></div>}
      {open && (
        <>
          <label>Certificate (PEM text)
            <textarea rows={8} value={pem} onChange={(e) => setPem(e.target.value)} placeholder="-----BEGIN CERTIFICATE-----" spellCheck={false} />
          </label>
          <p className="sub">Dapper tries the new certificate against your directory first. If it can't connect, nothing is changed.</p>
          <div className="formactions">
            <button type="button" className="primary" disabled={busy || !pem.trim()} onClick={async () => {
              setBusy(true); setMsg(null);
              try { setCA(await api.setCA(pem)); setPem(""); setOpen(false); setMsg({ kind: "ok", text: "The certificate was saved." }); }
              catch (x) { setMsg({ kind: "error", text: (x as Error).message }); } finally { setBusy(false); }
            }}>{busy ? "Testing…" : "Test and save"}</button>
            <button type="button" onClick={() => { setOpen(false); setPem(""); setMsg(null); }}>Cancel</button>
          </div>
        </>
      )}
    </div>
  );
}
