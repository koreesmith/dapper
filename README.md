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

## Try it in two minutes

Needs Docker.

```sh
git clone https://github.com/koreesmith/dapper && cd dapper
docker compose up -d --build
```

Open <http://localhost:8080> and sign in as **alice** / `alicepass` (administrator) or
**bob** / `bobpass` (regular user). This starts Dapper plus a throwaway demo OpenLDAP server
with sample people. The demo server is for trying things out only.

## Using it with your own LDAP server

1. Create a service account and a group for administrators: see [docs/service-account.md](docs/service-account.md).
2. Copy [`dapper.example.yaml`](dapper.example.yaml) to `dapper.yaml` and fill it in, or use environment
   variables: see [docs/configuration.md](docs/configuration.md).
3. Run the image, mounting a volume for settings:

```sh
docker build -t dapper .
docker run -p 8080:8080 -v dapper-data:/data \
  -e DAPPER_LDAP_URL=ldaps://ldap.example.org \
  -e DAPPER_LDAP_BIND_DN=cn=dapper-svc,dc=example,dc=org \
  -e DAPPER_LDAP_BIND_PASSWORD=... \
  -e DAPPER_USERS_BASE_DN=ou=people,dc=example,dc=org \
  -e DAPPER_ADMIN_GROUP_DN=cn=dapper-admins,ou=groups,dc=example,dc=org \
  dapper
```

Put it behind an HTTPS reverse proxy. Dapper itself speaks plain HTTP.

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
docker compose up -d openldap          # demo OpenLDAP on :389
cd backend && DAPPER_CONFIG=../dapper.yaml go run ./cmd/dapper    # API on :8080
cd web && npm install && npm run dev                              # UI on :5173, proxies /api
```

Tests:

```sh
cd backend && go test ./...            # unit tests
docker compose up -d --build && ./scripts/e2e.sh   # end-to-end, against the demo directory
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
