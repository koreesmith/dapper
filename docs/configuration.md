# Configuration

**Dapper is configured in its web interface.** There's no file to edit.

## First run

Start Dapper (for example `docker compose up -d --build`) and open it in a browser. Because nothing is
configured yet, you'll see a setup wizard. To prove you're the person who started it, the wizard asks for
a **setup code**, which Dapper prints in its log when it starts:

```sh
docker compose logs dapper
```

The wizard then walks through:

1. **Your directory:** the server address, whether to use StartTLS, and the service account
   ([what access it needs](service-account.md)). Dapper tests the connection straight away.
2. **Where things live:** the containers for people, groups and suspended people. Dapper finds the ones that
   already exist (and offers them as choices) and suggests names for any that don't.
3. **Administrators:** pick one of the groups already in your directory, make an existing person (found as
   you type) an administrator, or create a brand-new administrator account (handy for an empty directory).
4. **Linux accounts:** if people log in to Linux servers through this directory, Dapper detects your existing
   Unix accounts and suggests matching settings; see [production.md](production.md#6-things-to-know-about-a-live-directory).
5. **Review:** everything is tested against your directory, and you're shown every change Dapper would make
   (creating a missing container, the administrators group, a first administrator). Nothing is created until
   you approve it.

## Changing settings later

Administrators can change everything on the **Settings** page: the connection, where people and groups live,
the administrators group, Linux account options (ID range, home directory, shell, SSH keys), password rules and sign-out times, and which details people can see and change
about themselves. Settings are **tested before they're saved**, and a change that would lock you out of the
admin screens is refused. Changes take effect immediately, with no restart.

## Where settings are stored

In the data directory (`/data` in the Docker image): `settings.yaml` for connection and layout settings, and
`policy.yaml` for what people can change about themselves. **Keep this directory on a volume.** The
`settings.yaml` file contains the service account's password, so it is created readable by its owner only;
protect backups of it accordingly.

## Optional: configuring with environment variables

For automated deployments you can also set values with environment variables. A setting provided this way is
**locked**: the Settings page shows it read-only, and it can't be changed from the web interface.

| Environment variable | Setting |
|---|---|
| `DAPPER_LDAP_URL` | Server address (`ldap://host:389` or `ldaps://host:636`) |
| `DAPPER_LDAP_STARTTLS`, `DAPPER_LDAP_SKIP_VERIFY` | Encryption options |
| `DAPPER_LDAP_BIND_DN`, `DAPPER_LDAP_BIND_PASSWORD` | Service account |
| `DAPPER_USERS_BASE_DN` | Where people are |
| `DAPPER_USERS_SUSPENDED_BASE_DN` | Where suspended people go |
| `DAPPER_USERS_LOGIN_ATTRIBUTES` | Comma-separated, e.g. `uid,mail` |
| `DAPPER_GROUPS_BASE_DN` | Where groups are |
| `DAPPER_ADMIN_GROUP_DN` | The administrators group |

When all of the required ones (server, service account and password, people container, administrators group)
are set, the setup wizard is skipped. See `docker-compose.preconfigured.yml` for an example.

Process-level options always come from the environment: `DAPPER_LISTEN` (default `:8080`),
`DAPPER_DATA_DIR` (default `data`), `DAPPER_SETUP_CODE` (fix the setup code instead of a random one),
and `DAPPER_CONFIG` (path to an optional YAML file with the same structure as `settings.yaml`).

## Suspending accounts

- **With a "suspended people" container set** (recommended; works on any server), suspending moves the
  account there and restoring moves it back. Group memberships follow.
- **Without one**, Dapper locks the account in place using `pwdAccountLockedTime`, which needs OpenLDAP's
  `ppolicy` overlay.

## Running behind HTTPS

Dapper speaks plain HTTP. Put it behind a reverse proxy that terminates TLS and forwards
`X-Forwarded-Proto: https`; the session cookie is then marked `Secure` automatically.
