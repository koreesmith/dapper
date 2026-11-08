# Configuration

Dapper reads a YAML file (`dapper.yaml` by default, or the path in `DAPPER_CONFIG`)
and then applies environment variables on top. See [`dapper.example.yaml`](../dapper.example.yaml)
for every option with comments.

## Required

| Setting | Environment variable | Meaning |
|---|---|---|
| `ldap.url` | `DAPPER_LDAP_URL` | `ldap://host:389` or `ldaps://host:636` |
| `ldap.bindDN` | `DAPPER_LDAP_BIND_DN` | The service account (see [service-account.md](service-account.md)) |
| `ldap.bindPassword` | `DAPPER_LDAP_BIND_PASSWORD` | Its password. Prefer the environment over the file. |
| `users.baseDN` | `DAPPER_USERS_BASE_DN` | Where people live |
| `admin.groupDN` | `DAPPER_ADMIN_GROUP_DN` | Members of this group get the admin screens |

## Optional

| Setting | Environment variable | Default |
|---|---|---|
| `listen` | `DAPPER_LISTEN` | `:8080` |
| `dataDir` | `DAPPER_DATA_DIR` | `data` (the Docker image uses `/data`) |
| `ldap.startTLS` | `DAPPER_LDAP_STARTTLS` | `false` |
| `ldap.skipVerify` | `DAPPER_LDAP_SKIP_VERIFY` | `false`. Turns off certificate checks; only for testing. |
| `users.loginAttributes` | `DAPPER_USERS_LOGIN_ATTRIBUTES` (comma-separated) | `uid,mail` |
| `users.objectClasses` | | `inetOrgPerson` |
| `users.rdnAttribute` | | `uid` |
| `users.suspendedBaseDN` | `DAPPER_USERS_SUSPENDED_BASE_DN` | empty: lock in place with ppolicy |
| `groups.baseDN` | `DAPPER_GROUPS_BASE_DN` | empty: group management is off |
| `groups.objectClass` / `groups.memberAttribute` | | `groupOfNames` / `member` |
| `password.minLength` | | `8` |
| `session.idleTimeout` / `session.maxAge` | | `30m` / `8h` |
| `session.secureCookie` | | automatic (on when served over HTTPS) |

## Settings changed in the web UI

The **Settings** page lets administrators choose which details people can see and
change about themselves. Those choices are stored in `<dataDir>/policy.yaml`, so
mount `dataDir` on a volume if you run Dapper in a container.

## Suspending accounts

- **With `users.suspendedBaseDN` set** (recommended; works on any server), suspending moves the
  account into that container and restoring moves it back. Group memberships are updated to follow.
  Create the container once (for example `ou=suspended`).
- **Without it**, Dapper locks the account in place by setting `pwdAccountLockedTime`, which needs
  OpenLDAP's `ppolicy` overlay.

## Running behind HTTPS

Dapper speaks plain HTTP. Put it behind a reverse proxy that terminates TLS and forwards
`X-Forwarded-Proto: https`; the session cookie is then marked `Secure` automatically.
