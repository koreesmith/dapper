# Dapper

An open source web interface for managing an LDAP directory, designed so that people can
manage users **without knowing LDAP**. The first release targets **OpenLDAP**.

**For everyone**
- Sign in with your directory username (or email) and password
- Update your own contact details and change your password, limited to what your administrators allow

**For administrators**
- A searchable, paginated list of everyone. Search by name, username, email or phone, in plain words
- Add people, edit them, suspend and restore them, reset passwords, delete them
- Manage groups and membership
- Choose which details people can see and change about themselves
- An **Advanced** view for working with raw LDAP entries when you need to

Light, Dark and Sepia themes. Everything is built to work with your existing server: nothing
is installed into the directory and Dapper keeps no user database of its own.

## Getting started

Needs Docker. This starts **only Dapper**; it never starts or installs an LDAP server for you.

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
