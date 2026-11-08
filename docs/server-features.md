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
- **Not included yet:** `lastbind`, `syncprov` (replication) and `dynlist` (dynamic groups).
