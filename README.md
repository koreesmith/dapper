# Dapper

An open source web interface for managing an LDAP directory, designed so that people can
manage users **without knowing LDAP**. The first release targets **OpenLDAP**.

**For everyone**
- Sign in with your directory username (or email) and password
- Update your own contact details and change your password, limited to what your administrators allow

**For administrators**
- A searchable, paginated list of everyone. Search by name, username, email or phone, in plain words
- Add people, edit them, suspend and restore them, reset passwords, delete them
- **Linux-ready accounts:** new accounts get a UID/GID from your range, a home directory, login shell and a
  private group, so they work for logins on your Linux servers; optionally manage SSH public keys
- Manage groups and membership, including Linux groups and Red Hat-style personal groups; find and fix group-number mismatches
- Organise people into **units** (departments or sites), move them between units, add profile pictures
- **Bulk actions** (suspend, restore, delete, add to a group) and **CSV import and export**
- An **Activity** page: a searchable record of sign-ins and administrative actions
- **Server features:** switch on memberOf, password rules and lockout, duplicate prevention, change logging, last sign-in and replication with a few clicks (OpenLDAP overlays), plus schemas, indexes, access rules and the TLS certificate, each previewed first with a backup saved
- Choose which details people can see and change about themselves
- **No LDAP server yet? Dapper can run one for you:** an OpenLDAP server in its own container, set up through the
  same wizard, encrypted connections only, with scheduled backups, one-click restore steps and ready-made
  settings for your Linux machines ([docs/dapper-server.md](docs/dapper-server.md))
- An **Advanced** view for working with raw LDAP entries when you need to

Light, Dark and Sepia themes. Everything is built to work with your existing server: nothing
is installed into the directory and Dapper keeps no user database of its own.

## Quickstart

You need Docker. Five minutes, no LDAP knowledge required.

1. **Get Dapper and start it** (this starts only Dapper, never an LDAP server):
   ```sh
   git clone https://github.com/koreesmith/dapper && cd dapper
   docker compose up -d --build
   ```
   Port 8080 taken? Copy `.env.example` to `.env` and set `DAPPER_PORT=8585`.
2. **Read the setup code** Dapper prints on first start:
   ```sh
   docker compose logs dapper | grep -i "setup code"
   ```
3. **Open <http://localhost:8080>** and follow the setup wizard: enter the setup code, your LDAP server's address
   (for example `ldap://ldap.example.com:389`), a service account and its password
   ([what access it needs](docs/service-account.md)), and where your people and groups live. The wizard finds
   existing containers, groups and people, and asks before creating anything.
4. **Sign in** with your directory username and password. Administrators land on the People list.
5. **Next steps:** add a person, create a group, and visit **Server** to switch on features such as memberOf
   (needs the server's configuration login, see [docs/server-features.md](docs/server-features.md)).

Put Dapper behind an HTTPS reverse proxy for real use ([docs/production.md](docs/production.md)).

### No LDAP server yet?

Let Dapper run one. This starts Dapper **and** a new OpenLDAP server:

1. **Name the server.** Copy `.env.example` to `.env` and set `LDAP_HOSTNAME` to the name your other machines will
   use to reach it (for example `ldap.example.com`). It goes into the server's certificate.
2. **Start both:**
   ```sh
   docker compose -f docker-compose.yml -f docker-compose.server.yml up -d --build
   docker compose -f docker-compose.yml -f docker-compose.server.yml logs dapper | grep -i "setup code"
   ```
3. **Open <http://localhost:8080>**, enter the setup code and choose **I need a new one**. Give your organization
   and domain, create the first administrator, and approve the list of what Dapper will create.
4. **Save the two passwords** shown at the end (they appear only once), then follow the on-screen steps to trust the
   server's certificate and point your Linux machines at it.

Backups run daily and appear on the **Server** page; see [docs/dapper-server.md](docs/dapper-server.md) for restoring
and upgrading.
Just want to look around? See "Just want to look around?" below for a throwaway demo server.

## Getting started

Needs Docker. This starts **only Dapper**, for use with an LDAP server you already have. To have Dapper run a directory server for you as well, see "No LDAP server yet?" above.

```sh
git clone https://github.com/koreesmith/dapper && cd dapper
docker compose up -d --build
docker compose logs dapper        # shows the setup code
```

Open <http://localhost:8080>. A setup wizard asks for the setup code, then for your LDAP server's address and a
service account, and finishes by testing everything. All of Dapper's settings live in the web interface
afterwards. See [docs/configuration.md](docs/configuration.md), and
[docs/service-account.md](docs/service-account.md) for the access the service account needs.

Put Dapper behind an HTTPS reverse proxy for real use; it speaks plain HTTP itself.

### Just want to look around?

Add a throwaway demo OpenLDAP server with sample people:

```sh
docker compose -f docker-compose.yml -f docker-compose.demo.yml up -d --build
```

In the wizard use server `ldap://openldap:389`, service account `cn=admin,dc=example,dc=org` / `admin`, and the
existing administrators group `cn=dapper-admins,ou=groups,dc=example,dc=org`. Then sign in as **alice** /
`alicepass` (administrator) or **bob** / `bobpass` (regular user). The demo server is for trying things out only.

## Releases and upgrading

Releases are tagged `vX.Y.Z` (see [CHANGELOG.md](CHANGELOG.md)) and published as a container image,
`ghcr.io/koreesmith/dapper:<version>`. See [docs/upgrading.md](docs/upgrading.md) for upgrading and day-to-day
operation, and [SECURITY.md](SECURITY.md) to report a vulnerability.

## Security notes

- Passwords are verified by the directory itself; Dapper never stores them. Generated temporary
  passwords are shown once and not logged.
- Admin and self-service permissions are enforced on the server, not just hidden in the UI.
- Sign-in attempts are rate limited, sessions expire, and state-changing requests need a custom header
  (CSRF protection). Administrative actions are written to the log (`audit ...` lines).
- Dapper uses a privileged service account. Protect its password; see [docs/service-account.md](docs/service-account.md).

## Development

```text
backend/   Go API (go-ldap)
web/       React + TypeScript UI (Vite)
scripts/   end-to-end tests
```

```sh
docker compose -f docker-compose.yml -f docker-compose.demo.yml up -d openldap   # demo OpenLDAP on :389
cd backend && DAPPER_DATA_DIR=../data go run ./cmd/dapper                        # API on :8080 (prints a setup code)
cd web && npm install && npm run dev                                             # UI on :5173, proxies /api
```

Tests:

```sh
cd backend && go test ./...            # unit tests
# end-to-end, against the demo directory (fresh stack: runs the real first-run setup, then the main suite)
DAPPER_SETUP_CODE=test docker compose -f docker-compose.yml -f docker-compose.demo.yml up -d --build
SETUP_CODE=test ./scripts/e2e-setup.sh && ./scripts/e2e.sh
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
