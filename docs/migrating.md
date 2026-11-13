# Moving an existing directory to a server Dapper runs

If Dapper manages an LDAP server you run yourself, you can move that directory into a new OpenLDAP server that Dapper
runs ([dapper-server.md](dapper-server.md)). Your old server is only **read**, never changed. The new server gets a
copy with the **same directory name**, so every name, group, Linux account and client setting stays valid, and
people keep their passwords. You can undo the switch until you finish.

## Before you start

- Dapper is already set up and working against your current server.
- You have the old server's **directory manager** (the root account, for example `cn=admin,dc=example,dc=com`) and
  its password. Dapper needs it to read password hashes, uses it only for the check and copy, and never saves it.
- Optional but useful: on **Server**, unlock the server settings (the configuration login) first. Then Dapper can
  turn on the same features at the new server (memberOf, referential integrity, unique values, last sign-in) and list
  what it can't carry over.
- A place for the new server. **If your old server runs on the same machine** it already uses ports 389 and 636, so
  give the new one different ones in `.env` while you test (`LDAP_PORT=3389`, `LDAPS_PORT=3636`); after the cutover
  you can change them.

## Steps

1. **Add the new server next to Dapper.** Set `LDAP_HOSTNAME` in `.env` to the name your machines will use for it
   (it goes in the certificate), then:
   ```sh
   docker compose -f docker-compose.yml -f docker-compose.server.yml up -d --build
   ```
   Dapper and its data are kept; a new, empty server starts. A **Migrate** tab appears in Dapper.
2. **Check** (Migrate → 2). Enter the old server's directory manager. Dapper reads everything and tells you:
   - how many people, groups and units there are;
   - schema the new server doesn't know (object classes or attributes), and which entries use it. Add the schema to the
     new server first, or tick the box to leave those entries out;
   - how passwords are stored. The new server can check `{SSHA}`, `{SHA}`, `{SMD5}`, `{MD5}`, `{CRYPT}`, `{ARGON2}`,
     `{SSHA256/384/512}`, `{PBKDF2…}` and `{APR1}`. Anyone with another scheme must reset their password afterwards;
     plain-text passwords are copied as they are (reset them to get them stored safely);
   - what isn't carried over.
3. **Copy** (Migrate → 3). Approve and copy. Dapper creates the directory with the same name, turns on the carried
   features, copies everything (parents first, people before groups) with password hashes exactly as they are, and then
   **checks** that every entry on the new server is identical to the old one. It shows progress, and reports anything that
   failed or differs.
   **Keep the old directory manager** (ticked by default, recommended): the new server's directory manager is the account
   you entered for the old server, with the same password, so applications and machines that sign in as it keep working
   without any change. The login has to be inside the directory (a name like `cn=admin,cn=config` belongs to the old
   server's own configuration and can't be reused). Untick it to get a new, generated directory manager password instead,
   shown once after the switch; everything that signs in as the old directory manager then needs the new password.
   It can be run again: it adds what's new, updates what changed and never deletes. Run it once more just before the
   switch to pick up changes made in the meantime.
4. **Switch** (Migrate → 4). Dapper starts managing the new server, and everyone is signed out (people sign in again
   with the same passwords). You'll see, once, the new directory manager and configuration administrator passwords,
   a checklist, and the certificate. **Save the passwords.**
5. **Move your machines and applications** (the checklist): point DNS (or the address they use) at the new server,
   open ports 636 and/or 389, give every machine the server's certificate, update `sssd.conf` or `ldap.conf`, review
   the access rules.
6. **Roll back or finish.** After signing in again, the Migrate tab offers **Roll back to the old server** (Dapper
   returns to its saved connection; your old server was never changed) and **Finish the migration** (Dapper forgets the
   old server's saved connection and password; you can't roll back after that). Keep the old server running until
   everything works.

## What isn't carried over

- Access rules (ACLs). The new server starts with sensible defaults; review them under Server.
- Indexes, the server's certificate (the new server has its own), replication, and other settings in the old server's own
  configuration. Features other than memberOf, referential integrity, unique values and last sign-in (for example
  password rules, replication and change logging) are listed so you can set them up again under Server.
- The old server's own configuration login (for example `cn=admin,cn=config`). The new server has its own configuration
  administrator, `cn=config-admin,cn=config`, generated at the switch and shown once. The directory manager is kept
  (same name and password) unless you chose a generated one; everyone's own password is carried over either way.
- When each password was last changed, so password expiry clocks start again at the move.

## If something goes wrong

- **The copy reports failures or differences.** Read the entries listed, fix the cause (usually schema, or a parent
  entry missing on the old server) and copy again. Switching is blocked until a copy checks out.
- **Dapper restarted after the copy.** Run the copy again; it is quick and safe. (The new directory's generated password
  is only held in memory until the switch.)
- **After a rollback** the new server stays as it is, but its first-start file was used up, so it can't be copied to
  again. To try again from scratch, remove it and its data and start a new one:
  `docker compose -f docker-compose.yml -f docker-compose.server.yml down` and
  `docker volume rm <project>_ldap-config <project>_ldap-data <project>_ldap-tls <project>_ldap-handoff <project>_ldap-backups`
  (this deletes the new server's data; the old server is untouched).

## Limits

Directories up to 200,000 entries; the copy reads the whole directory into memory and goes entry by entry, so very large
ones take a while. Only OpenLDAP is tested as the old server.

## After the switch: applications that can't sign in

When the switch is done and DNS points at the new server, Linux machines and applications may fail to sign in even though
the data was copied exactly. In practice there are three causes, and the new server's own log tells you which one each
client is hitting. Try a login, then run this on the machine that runs the server (replace `<server container>`):

```sh
docker logs --since 15m <server container> 2>&1 | awk '
/ACCEPT from IP=/ {match($0,/conn=[0-9]+/); c=substr($0,RSTART,RLENGTH); match($0,/IP=[][0-9a-fA-F.:]+/); a=substr($0,RSTART+3,RLENGTH-3); sub(/:[0-9]+\]?$/,"",a); gsub(/[][]/,"",a); ip[c]=a}
/ BIND dn=/ {match($0,/conn=[0-9]+/); c=substr($0,RSTART,RLENGTH); match($0,/dn="[^"]*"/); dn[c]=substr($0,RSTART+4,RLENGTH-5)}
/RESULT tag=97 err=(49|13)/ {match($0,/conn=[0-9]+/); c=substr($0,RSTART,RLENGTH); match($0,/err=[0-9]+/); print ip[c], dn[c], substr($0,RSTART,RLENGTH)}
/TLS negotiation failure/ {match($0,/conn=[0-9]+/); c=substr($0,RSTART,RLENGTH); print ip[c], "-", "TLS negotiation failure (certificate not trusted)"}' | sort | uniq -c | sort -rn
```

Each line is a count, the client's address, the login it tried and the result.

| You see | Cause | Fix |
|---|---|---|
| `err=49` for `cn=admin,…` | The client still has the **old directory manager password**. The new server's was generated at the switch. | Give the client the new password (sssd: the Ansible role's bind password; applications: their LDAP settings). |
| `err=13` | The client connects **without encryption**, which the new server refuses. | Switch it to StartTLS on port 389 or LDAPS on port 636. |
| `TLS negotiation failure` | The client **doesn't trust the new certificate** (it is self-signed). | Install the certificate where the client looks for it. For `sssd`, `ldap_tls_cacert` (a bundle with the old and new certificates lets it trust either server while you can still roll back). For a container, mount the file in and point `TLS_CACERT` at it. For PHP on SELinux (Nextcloud), put the certificate in the system trust store with `update-ca-trust`, since a confined web process can't read files under `/etc/openldap`. |

Better than handing each application the directory manager: give each its own read-only account under **Service accounts**
([application-accounts.md](application-accounts.md)), and the directory manager's password stays out of every application.

Passwords of people (not the directory manager) were copied as they are, so they keep working once the client can
connect.

A note on testing a bind by hand: `ldapwhoami -y <file>` treats a trailing newline in the file as part of the password,
so a correct password appears to fail. Remove the newline (`printf %s`, or `tr -d '\n'`), or use `-W` and type it.
