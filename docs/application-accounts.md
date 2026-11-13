# Service accounts: read-only accounts for applications

Applications and machines that look people up in the directory (sssd on Linux servers, Nextcloud, Zabbix, Semaphore,
Gitea and so on) have to sign in to the directory themselves. Giving them the **directory manager** works, but it hands
every one of them full control of the directory, and changing that password later means changing it everywhere.

**Service accounts** are a better answer: a plain account per application (or per group of machines) that can look
people up and can't change anything.

## Making one

**Service accounts** (the tab next to Server) → **New service account**. Give it a short name (`sssd`, `nextcloud`…)
and say what it's for. Dapper lists exactly what it will create, and creates it when you approve:

- the container `ou=services` (once, the first time), under your directory's base name; and
- the account `cn=<name>,ou=services,…`, with a generated password shown **once** (or one you choose, at least 12
  characters). Dapper does not keep the password.

A service account is **not a person**: it doesn't appear in the People list, gets no home directory or Linux
account, and Dapper gives it no right to change anything.

When it is created Dapper **signs in as the account and checks it**, and tells you in plain words:

| Result | What it means |
|---|---|
| Signs in, reads people, can't see password hashes | Ready to use. |
| Signs in but **can't see any people** | The server's access rules don't let it read. See below. |
| Signs in and can read **password hashes** | The access rules are too open for an account like this. Tighten them under Server → Access rules. |
| Can't sign in | The password wasn't accepted. |

The page then shows the settings to give the application: the bind DN (the account's name), the server address, and
the search base.

## When it can't read the directory

Which accounts can read what is decided by the server's **access rules**. A server that only lets its manager read
(some default setups do) shows a new account nothing. A server that lets every signed-in account read (the default
on a server Dapper runs) needs nothing.

If the check says the account can't read, Dapper can add **one clause** to the catch-all access rule:
`by dn.children="ou=services,…" read`. Every account in the service container can then read what other signed-in
accounts can, and nothing else changes. To do it, unlock the server settings on the **Server** page (the separate
configuration login), come back, and use **Show me the change**. You see the exact change, Dapper saves a copy of the
current rules first, and it applies only when you approve.

Dapper refuses to add the clause when it can't be sure it is safe: if there is no catch-all rule to put it in, or if no
earlier rule protects `userPassword` (so adding read access could expose password hashes). In that case it tells you
the clause to add yourself.

## Resetting and deleting

- **Reset password** gives the account a new password (generated, or yours). Whatever uses the old one stops working
  until you update it, so do this when you've got the application's settings open.
- **Delete** removes the account. This page only ever changes entries inside the services container, so it can't be
  used on people or anything else.

## Using one

- **sssd** (`/etc/sssd/sssd.conf`): `ldap_default_bind_dn` is the account's DN and `ldap_default_authtok` its password.
- **Nextcloud**: LDAP/AD integration → Server → the user DN and password.
- **Zabbix**: Administration → Authentication → the LDAP server's bind DN and bind password.
- **Semaphore**: `SEMAPHORE_LDAP_BIND_DN` and `SEMAPHORE_LDAP_BIND_PASSWORD`.

Most applications also need the server's certificate trusted and an encrypted connection; see
[dapper-server.md](dapper-server.md) and, after a migration, [migrating.md](migrating.md).
