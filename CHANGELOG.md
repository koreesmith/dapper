# Changelog

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
