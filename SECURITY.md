# Security policy

Dapper manages the accounts and passwords in your directory, so security reports are taken seriously.

## Reporting a vulnerability

Please **don't open a public issue** for a security problem. Report it privately through GitHub's
"Report a vulnerability" button on the repository's Security tab (or email the maintainers listed there), with:

- what you found and which version (`dapper -version`, or the footer of the page),
- how to reproduce it, and
- what an attacker could do with it.

You'll get an acknowledgement, and a fix or mitigation plan, as quickly as the maintainers can manage.
Please allow time to release a fix before disclosing publicly.

## How Dapper protects your directory

- **Passwords are verified by your directory**, never stored by Dapper. Generated temporary passwords are shown
  once and never logged.
- **Permissions are enforced on the server**, not just hidden in the page: administrator pages, what people may
  change about themselves, and every directory change are checked on each request.
- **Sessions** are random, expire when idle (default 30 minutes) and after 8 hours, and end immediately when an
  account is reset, suspended, deleted, loses administrator rights or changes its password.
- **Sign-in attempts are rate limited** per account and per address; state-changing requests need a custom header
  (CSRF protection); responses carry security headers.
- **Anything that changes your directory is previewed and needs approval**, and administrative actions are
  written to the log (and shown on the Activity page).
- **The service account's password** is stored owner-only in the data volume, is never sent to the browser, and is
  never reused for a different server or account than the one it was saved for.
- **Server configuration changes** need a separate login that is kept in memory only, and configuration backups leave
  out password hashes.

## Deploying safely

- Put Dapper behind an HTTPS reverse proxy; it speaks plain HTTP itself (see `docs/production.md`).
- Connect to your directory with LDAPS or StartTLS, and don't switch certificate checking off outside testing.
- Give the service account only the access it needs (`docs/service-account.md`) and a long random password.
- Run a single Dapper instance (sessions are kept in memory) and keep the data volume private and backed up.
