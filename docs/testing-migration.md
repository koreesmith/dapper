# Testing a migration: a runbook

For trying the migration (DAP-52) on a UAT server before using it for real. The old server is only read, so a
migration test never changes it; the one thing that changes is Dapper's own settings (and you can roll those back).

## 0. Before you start

- **Back up Dapper's data volume** (it holds the settings, including the old server's service-account password):
  `docker run --rm -v dapper_dapper-data:/data -v "$PWD":/backup alpine tar czf /backup/dapper-data.tgz -C /data .`
  (volume name = your Compose project name + `_dapper-data`).
- Have the old server's **directory manager** name and password, and (optional) its configuration login.
- If the old server is on the **same machine**, set `LDAP_PORT=3389` and `LDAPS_PORT=3636` in `.env`, so the new server
  doesn't clash with ports 389 and 636.
- Set `LDAP_HOSTNAME` in `.env` to a real name for the new server (or anything for a trial).

## 1. Add the new server

```sh
docker compose -f docker-compose.yml -f docker-compose.server.yml up -d --build
```

Open Dapper and sign in as before. **Expect:** a **Migrate** tab. Open it: step 1 says it found the new server and shows
the name it will be known by. If not, it shows the command above and a **Check again** button.

## 2. Check

On **Server**, unlock the settings (configuration login) first if you want features carried over. Then Migrate → 2 → enter
the directory manager → **Check the old server**.

| Look at | Expect |
|---|---|
| Counts | match what you expect (people, groups, units) |
| Password storage | the schemes you use, all "supported", or a warning listing who would need a reset |
| Schema warnings | none; or the object classes/attributes the new server lacks (e.g. `sudoRole`) and how many entries use them |
| Features | the ones on at the old server and carryable |
| "What isn't copied" | read it; this is your to-do list |

Try a **wrong password** too: you should get a plain message.

## 3. Copy and check

Tick the approval and **Copy now**. **Expect:** progress, then "The copy is finished and checked: every entry on the new
server is identical to the old one." Note the counts. Press **Copy again**: it should add nothing and change nothing
(or only what changed at the old server since). Change a person at the old server and copy again: that person is
updated.

## 4. Spot-check the copy

Dapper has already compared every entry. For your own peace of mind, note the counts it reports (added, updated, already the
same) against the check's totals. The new directory's manager password is only shown at the switch, so a direct look with
`ldapsearch` (compare `ldapsearch -x -LLL -b <base> dn | grep -c ^dn` on both servers) is possible right after step 5.

## 5. Switch

Tick the approval, **Switch**. **Expect:** two passwords shown once (save them), a checklist, the certificate; you are signed
out.

Sign in again with your usual password. **Check:**

- someone with a **hashed** password signs in with their old password;
- a Linux machine can still look up users and sign in (after you follow the checklist for that machine: DNS or address,
  certificate, `sssd.conf`);
- groups and memberships look right, and **memberOf** shows if it was on;
- the **Server** page works with the new configuration login;
- **Server → Backups** appears; make a backup.

## 6. Roll back (try it)

Migrate → **Roll back to the old server**. **Expect:** everyone signed out, Dapper managing the old server again, same
people, same passwords. Nothing changed at the old server. (To try a second time from scratch, see "If something goes wrong"
in [migrating.md](migrating.md).)

## 7. Finish (when you're sure)

Migrate → **Finish the migration**. **Expect:** the Migrate tab goes away. Dapper has forgotten the old server's saved
connection, and rolling back is no longer possible.

## What to report

For each step: what you did, what you saw, and, if it differed, the output of `docker compose logs dapper | tail -80`
(no passwords are logged) and a screenshot. Don't paste password hashes or backup files.
