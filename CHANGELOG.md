# Changelog

## 2.0.0

Dapper can now run your directory server too. Everything from 1.0.0 still works unchanged with a server you run
yourself.

### A directory server run by Dapper
- **Choose at setup:** "I already have an LDAP server" (the 1.0.0 steps, unchanged) or "I need a new one".
- **New image:** `ghcr.io/koreesmith/dapper-ldap`, OpenLDAP 2.6 on Debian, built by us and scanned for known
  vulnerabilities in CI. It includes the memberOf, refint, ppolicy, unique, auditlog, lastbind and syncprov overlays
  and the core, cosine, inetorgperson, nis and openssh-lpk schemas. New passwords are stored as Argon2; hashes from
  elsewhere (for example `{SSHA}`) keep working.
- **`docker-compose.server.yml`** runs Dapper and the server together (the 1.0.0 `docker-compose.yml` is unchanged).
  Settings go in `.env` (`LDAP_HOSTNAME`, `LDAP_TLS_NAMES`, ports, backup schedule).
- **Encrypted connections only:** a self-signed certificate is made on first start (or bring your own); plain,
  unencrypted binds are refused.
- **Guided setup:** name your organization and domain, create the first administrator, choose server features, review
  and approve what will be created. Passwords are generated and shown once.
- **Connect your machines:** address, directory name, the certificate to trust and a starting `sssd.conf`, on the
  final setup screen and on the Server page afterwards.
- **Backups and restore:** the server backs up daily (configurable), keeps the newest 14, and Dapper lists them, makes
  one on request, and offers downloads (which need the server settings unlocked). Restore is a documented three-command
  procedure that saves the current state first. See [docs/dapper-server.md](docs/dapper-server.md).
- **Trusted certificate** setting: give Dapper the certificate of a server that uses its own (self-signed), tested
  before it is saved. Works with servers you run yourself too.

### Migrate an existing directory to a server Dapper runs
- A **Migrate** tab (when Dapper manages a server it doesn't run). **Check** reads the old directory and reports what
  will fit: counts, schema the new server doesn't know, how passwords are stored, and what isn't carried over.
  **Copy** creates a directory with the same name on the new server and copies every entry with password hashes exactly as
  they are, then checks that the copy is identical. It can be repeated to pick up changes. **Switch** repoints Dapper and
  shows the new passwords once with a cutover checklist; **Roll back** returns to the old server; **Finish** forgets it.
  See [docs/migrating.md](docs/migrating.md).
- The Dapper-run server also checks more password schemes (SSHA256/384/512, PBKDF2, APR1) so people carried over from
  other servers can keep signing in.

### Smaller improvements
- People with a picture show it (a small thumbnail) beside their name on the People page.
- If your session ends, Dapper takes you to the sign-in screen and back to the page you were on, instead of showing a
  "not signed in" error.
- Fixed: a search term with letters and digits, such as `bk555`, no longer matches every phone number containing
  those digits.
- The refint overlay now writes its attribute names one per value, as OpenLDAP 2.6 expects.

### Known limits
- Migration doesn't copy access rules, indexes or other server configuration, and password expiry clocks restart.
- No replication or high availability for a Dapper-run server; it is one container.
- Dapper can't stop or start the server container itself (it has no Docker access by design), so restoring and
  upgrading the server image use a few `docker compose` commands that Dapper shows you.
- Docker only (no Podman or Kubernetes yet).

## 1.0.0

The first release of Dapper: a web interface for managing an OpenLDAP directory without knowing LDAP.

### For everyone
- Sign in with your directory username or email; update your own contact details; change your password with the
  directory's rules and expiry shown to you; upload or remove your own picture when your administrators allow it.
- Light, Dark and Sepia themes; works at phone width and passes an automated accessibility scan in all three themes.

### For administrators
- A searchable, paginated People list (plain-word search by name, username, email or phone); add, edit, suspend,
  restore, reset the password of, and delete accounts.
- **Bulk actions** on the list (suspend, restore, delete, add to a group), **CSV export** of the current search, and a
  checked **CSV import** that shows every problem before anything is created and gives temporary passwords once.
- **Linux accounts:** UID/GID from a configured range, private group per person, home directory and login shell,
  optional SSH public keys and shadow account. Change a person's primary group from their page.
- **Groups:** shared groups, Linux (posixGroup) groups and Red Hat-style personal groups; membership; change a group's
  ID and find and fix group-number mismatches.
- **Units:** create, rename (when empty) and remove units; create people in a unit; move people between units keeping
  their groups.
- **Profile pictures** for anyone, validated and re-encoded.
- **Activity:** a searchable record of sign-ins and administrative actions, with CSV download.
- Choose which details people can see and change about themselves.
- **Server page** (needs the server's configuration login, kept in memory only): point-and-click overlays (memberOf,
  referential integrity, password rules, duplicate prevention, change log, last sign-in, replication provider), plus
  schemas (including one-click SSH key support), indexes, access rules, the served TLS certificate and its file paths,
  and read-only replication status. Every change is previewed, backed up and approved first.
- An **Advanced** view for raw LDAP entries.

### Setup and operation
- First-run setup wizard (guarded by a setup code printed in the log) that finds existing containers, groups and people
  and asks before creating anything; all settings are then edited in the web interface.
- Published as a container image, `ghcr.io/koreesmith/dapper:<version>`; the version is shown in the footer and at
  `/api/version`.
- See [docs/upgrading.md](docs/upgrading.md) for upgrading and day-to-day operation.

### Security
- Passwords are verified by the directory and never stored by Dapper; sessions end on password change, reset, suspend,
  delete and demotion; sign-in attempts are rate limited; state-changing requests need a custom header; secrets and
  replication credentials are never sent to the browser or written to backups; the saved service password is never
  reused for a different server; StartTLS verifies the server name. See [SECURITY.md](SECURITY.md).

### Known limits
- Email-based password reset is not included yet.
- Sessions are in memory: run a single instance; restarting signs everyone out.
- The People list loads up to 5,000 accounts per search and filters in memory.
- OpenLDAP 2.4 can't remove an overlay while running, so switching one "off" pauses it.
- Targets OpenLDAP; other directory servers are not supported yet.
