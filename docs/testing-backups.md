# Testing backups and restore: a runbook

For trying out backups on a Dapper-run directory server (DAP-50, DAP-64, DAP-65) before you rely on them.
Allow about 30 minutes. Do it on a **test stack first**, not the server you care about.

Every command is run in the folder where you cloned Dapper. To save typing:

```sh
C="docker compose -f docker-compose.yml -f docker-compose.server.yml"
```

If you run the test stack next to another Dapper, put these in `.env` first so the ports don't clash:
`DAPPER_PORT=18080`, `LDAP_PORT=3389`, `LDAPS_PORT=3636`, `LDAP_HOSTNAME=ldap.test`.

## 0. Start clean

```sh
$C down -v          # throws away the test stack's data (only use this on a TEST stack)
$C up -d --build
$C logs dapper | grep -i "setup code"
```

Open Dapper, enter the code, choose **I need a new one**, and finish setup (any organization, `test.lan`, a first
administrator). Save both passwords shown at the end. You need the configuration one in step 4.

Add a few people so there is something to lose: **People → New person**, three or four of them, and put one in a
group (**Groups**).

## 1. A backup on request

1. **Server → Backups → Back up now.**
2. The button says "Backing up…" for a few seconds, then a new row appears: kind **Made on request**, a size of a
   few KB, and "Last successful backup" shows the time.

**Expect:** no warning banner. **If not:** `$C logs openldap | grep dapper-ldap` shows what the backup loop said.

Look inside it (optional):

```sh
$C run --rm --no-deps --entrypoint sh openldap -c 'ls -l /backups; f=$(ls /backups/dapper-ldap-manual-* | tail -1); tar -tzf $f; tar -xzOf $f ./manifest'
```

**Expect:** `config.ldif`, `data-1.ldif` and a manifest with `database1=dc=test,dc=lan` and an `entries1=` count that
matches the number of entries you have (people + groups + the containers + the top entry).

## 2. Downloads are protected

1. In the Backups table, the last column says **Unlock to download**.
2. Scroll to **Unlock server settings**, enter `cn=config-admin,cn=config` and the **configuration password** from
   setup, and unlock.
3. The column now says **Download**. Download one and check it opens: `tar -tzf <file>`.
4. **Activity** shows `backup.request` and `backup.download` lines.

**Expect:** you can't download until you unlock. **Why:** a backup contains every password hash.

## 3. The schedule

The server backs up every 24 hours by default. To see it happen without waiting, make it think the last backup was
long ago, then restart it:

```sh
$C run --rm --no-deps --entrypoint sh openldap -c 'echo "last_attempt=1" > /backups/.loop-state; echo "last_success=" >> /backups/.loop-state; echo "last_error=" >> /backups/.loop-state'
$C restart openldap
```

Wait about 15 seconds, then reload the Backups section. **Expect:** a new row of kind **Scheduled**.

Retention: set `LDAP_BACKUP_KEEP=3` in `.env`, `$C up -d`, make four backups with **Back up now**, and check only
three remain (the "saved before a restore" ones from step 5 are never removed).

## 4. The overdue warning

Make every backup look old by renaming them (the date is in the file name), then reload the page:

```sh
$C run --rm --no-deps --entrypoint sh openldap -c 'cd /backups; i=0; for f in dapper-ldap-*.tar.gz; do i=$((i+1)); mv "$f" "$(echo "$f" | sed -E "s/[0-9]{8}-[0-9]{6}/20260101-00000$i/")"; done; ls'
```

**Expect:** an amber warning: "The newest backup is more than twice as old as the schedule says it should be."
Then press **Back up now**; the warning goes away.

## 5. Restore (the important one)

1. **Make a known state.** Note who is in **People** and the group memberships. Press **Back up now** and wait for
   the row. Write down its file name (the **Restore from a backup** section shows it in the commands).
2. **Cause damage.** After the backup: delete one person, add a new person (call them `late`), and change someone's
   password.
3. **Open Server → Backups → Restore from a backup,** pick that backup, and run the three commands it shows, from
   your terminal. They look like:

   ```sh
   $C stop openldap
   $C run --rm openldap restore dapper-ldap-manual-<date>.tar.gz
   $C up -d
   ```

   **Expect** from the second command: `Saving the current state first...`, `Restoring ...`, `restored dc=test,dc=lan`,
   `Done. Start the server again.`
4. Wait until `$C ps` shows `openldap` as healthy, then reload Dapper (you may be signed out; sign in again).

**Check each of these:**

| Check | Expected |
|---|---|
| The person you deleted | back |
| The person `late` | gone |
| The changed password | the old one works again; the new one doesn't |
| The group and its members | as at the backup |
| **Server → Backups** | a new row, kind **Saved before a restore** |
| Sign in as the first administrator | works |
| **Server → unlock** with the configuration login | works (same password as before) |
| Another machine or `ldapsearch` over TLS | works, certificate unchanged |

**Undo the restore** (optional, proves the safety net): run the same three commands but with the **Saved before a
restore** backup. `late` should come back.

## 6. When it goes wrong

Try these on purpose so you know what they look like:

- **Wrong file name:** `$C run --rm openldap restore nothing.tar.gz` prints `No such backup` and lists what there is.
  Nothing is changed.
- **A path instead of a name:** `restore ../x` is refused.
- **Not a backup:** put any other file in `/backups`, restore it: "isn't a Dapper directory backup", nothing changed.
- **Restoring while the server is running:** don't. The first command (`stop`) matters. If you skipped it, stop and
  restore again; the saved-before copy makes this recoverable.
- **Backups folder not shared:** if the Backups section says Dapper can't see the backup folder, check that
  `docker-compose.server.yml` still mounts `ldap-backups` into both `openldap` and `dapper`.

## 7. Copy a backup off the machine and use it elsewhere

```sh
$C run --rm --no-deps --entrypoint sh openldap -c 'ls /backups'                       # pick a name
docker cp "$($C ps -q openldap)":/backups/<name> .                                    # copy it out
```

Or download it from the Backups table after unlocking. To restore on a **different** machine, start a fresh Dapper-run
server there (complete setup, or just start it), copy the file into its `/backups` (`docker cp <file> <container>:/backups/`),
and run the restore commands. The configuration login and directory data come from the backup; the certificate there
stays as it is, so machines will need to trust that server's certificate.

## 8. Clean up

```sh
$C down -v
```

## What to report

For each step: what you did, what you saw, and if it differed from **Expect**, the output of
`$C logs openldap | tail -50` and `$C logs dapper | tail -50`. Passwords and hashes will not be in the logs; don't
paste backup files.
