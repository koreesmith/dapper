# Server features (OpenLDAP overlays)

The **Server** page switches on OpenLDAP features (called *overlays*) without editing configuration files.
Every change is explained in plain words and previewed first, the server's current configuration is saved
before anything changes, and nothing happens until you approve it.

| Feature | What it does | Why you'd want it |
|---|---|---|
| **memberOf** | Every person's entry lists the groups they belong to | Applications such as GitLab, Jenkins, Grafana and VPNs read group membership from the person's own entry |
| **Referential integrity (refint)** | Groups are updated when someone is renamed, moved or deleted | Stops groups pointing at people who no longer exist |
| **Password rules and lockout (ppolicy)** | Minimum length, expiry, history, lockout after wrong passwords | Real password security for every application using the directory |
| **Prevent duplicates (unique)** | Refuses a second person with the same username, email or user ID | Duplicate user IDs cause confusing problems on Linux systems |
| **Change log (auditlog)** | Writes every change to a file on the directory server | Security reviews and "what just changed?" |
| **Last sign-in (lastbind)** | Records when each person last signed in | Finding accounts nobody uses. A contributed extra: your server package may not include it |
| **Allow copying (syncprov)** | Lets other servers keep a copy of this one | The first step of replication |

## Unlocking

These features live in the directory server's own configuration (`cn=config`), which needs a separate
administrator login. Enter it on the Server page; Dapper keeps it **in memory for that visit only**, never
saves it, and forgets it after 15 idle minutes or when you press Lock.

To create that login, run `slappasswd` on the directory server and apply this with
`sudo ldapmodify -Y EXTERNAL -H ldapi:///` (replace the hash):

```
dn: olcDatabase={0}config,cn=config
changetype: modify
replace: olcRootDN
olcRootDN: cn=admin,cn=config
-
replace: olcRootPW
olcRootPW: {SSHA}PASTE-A-HASH-HERE
```

(`replace` works whether or not the settings already exist.)

### If unlocking fails

"The directory server didn't accept that name and password" means it answered but doesn't recognise that pair.
This login is **separate from your directory manager's**. Check on the directory server:

```sh
# What config login exists, and is a password set for it?
sudo ldapsearch -Y EXTERNAL -H ldapi:/// -LLL -b 'olcDatabase={0}config,cn=config' -s base olcRootDN olcRootPW
# Try the exact name and password from the server itself
ldapwhoami -x -H ldap://localhost -D 'cn=admin,cn=config' -W
```

No `olcRootDN` or `olcRootPW` line means no remote config login exists yet: create it with the LDIF above. The name
you type into Dapper must match `olcRootDN` exactly. Dapper writes the server's answer (never the password) to its log:
`docker compose logs dapper | grep "unlock failed"`.

The directory server must accept connections from the machine running Dapper (use LDAPS or StartTLS: this
login can change the whole server).

**Prefer not to unlock at all?** Every feature offers *Show me how to turn this on*: Dapper writes out the exact
LDIF and the command to run on the server yourself.

## Good to know

- **"Off" means paused.** OpenLDAP 2.4 refuses to remove an overlay while the server is running, so turning one
  off clears its key setting (for example the password policy it points at) and it stops doing anything. The module
  stays loaded; turn it on again at any time. **memberOf** has no such off switch: it can only be removed by editing the
  server configuration and restarting, and Dapper says so rather than pretending. It's harmless to leave on.
- **memberOf must match your groups.** It only tracks one group type and member attribute. Dapper compares that with the
  groups it manages and tells you when they differ (the OpenLDAP demo image, for instance, ships watching
  `groupOfUniqueNames`). Use *Change* to fix it, then *Refresh existing groups* so current members show memberOf straight
  away: memberOf otherwise only notices changes made after it was switched on.
- **Password rules** are stored as an ordinary entry in your directory (by default `cn=default,ou=policies,<suffix>`);
  Dapper creates it, and its `ou=policies` container, with your approval. Lockout applies to **every** account, including
  service accounts used by your applications (the directory's root account is never locked): check before turning it on.
  An administrator unlocks a locked account with **Restore** on the person's page.
- **Unique** only stops new duplicates. Dapper lists people who already share a value before you turn it on, but doesn't
  change them.
- **Modules** must be installed on the directory server (normally part of the `slapd` / `openldap-servers` package or its
  contrib add-on). If the server can't load one, Dapper shows the server's own message.
- **Backups** of the affected configuration are written to `<dataDir>/backups/` before every change and can be downloaded
  from the confirmation message.
- **Not included yet:** `dynlist` (dynamic groups).
- **`lastbind` and `syncprov` can't be switched off** while the server runs, like memberOf, and Dapper says so.

## Other server settings

Once unlocked, the Server page also shows and manages these. Every change is previewed as LDIF, backed up to
`<dataDir>/backups/` first, and needs your approval, like the overlays above.

- **Schemas.** Lists what the server has loaded. If your server doesn't understand SSH public keys, *Add SSH key
  support* adds the standard OpenSSH `openssh-lpk` schema (needed for the SSH keys option under Linux accounts).
  Schemas can't be removed while the server runs.
- **Search speed (indexes).** Shows the indexes and lets you add one (exact, contains, exists, sounds-like) or remove
  one. The indexes the server needs itself (`objectClass`, `entryCSN`, `entryUUID`) can't be removed here. Adding an index
  to a directory that already has data may not cover existing entries until the server re-indexes (`slapindex`, with the
  server stopped), so check a search afterwards.
- **Access rules.** Shows who may read and change what, and lets you replace the rules. Dapper checks each rule's shape and
  warns when the set looks like it could lock people out (no rule for sign-ins, nothing about `userPassword`, or the
  service account not named and not the root account). It cannot judge whether the rules are *right*: a wrong rule set can
  need fixing on the server itself, so read the preview carefully.
- **Encryption (TLS).** Connects the way Dapper does and shows the certificate the server really presents (name, issuer,
  expiry, whether it is self-signed or expired). You can change the certificate, key and authority file paths; the files
  must already exist on the directory server and be readable by it. Depending on your version the server may need a
  restart before it uses new files.
- **Replication.** Read-only: whether this server provides or copies data, the server ID, and the `contextCSN` values to
  compare between servers. Replication credentials are never shown or written to backups.
