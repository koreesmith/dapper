# Dapper

An open source web GUI for managing LDAP servers. The first release targets **OpenLDAP**.

## Features (v1.0.0 MVP)

- Connect and bind (LDAP, LDAPS, StartTLS)
- Browse the directory tree
- View and edit entry attributes
- Search with LDAP filters
- Create and delete entries (API; UI in progress)

## Layout

- `backend/` – Go HTTP/JSON API (`go-ldap`)
- `web/` – React + TypeScript UI (Vite)

## Development

```sh
make dev-ldap   # test OpenLDAP on :389 (admin: cn=admin,dc=example,dc=org / admin)
make backend    # API on :8080
make web        # UI on :5173, proxies /api to the backend
```

## Docker

```sh
docker build -t dapper .
docker run -p 8080:8080 dapper
```

## License

Apache License 2.0. See [LICENSE](LICENSE).
