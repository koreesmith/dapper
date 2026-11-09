# Running your directory server with Dapper

If you don't have an LDAP server, Dapper can come with one. It is OpenLDAP in its own container, built from
the `openldap/` folder, and it starts empty: Dapper's setup walks you through the rest.

## Start it

```sh
docker compose -f docker-compose.yml -f docker-compose.server.yml up -d --build
docker compose -f docker-compose.yml -f docker-compose.server.yml logs dapper    # the setup code is here
```

Set `LDAP_HOSTNAME` in `.env` first (see `.env.example`) to the name your servers will use to reach this one, for
example `ldap.example.com`. It is written into the server's certificate. Changing it later means making a new
certificate.

## What you get

- OpenLDAP 2.6 with the memberOf, refint, ppolicy, unique, auditlog, lastbind and syncprov overlays available, and
  the core, cosine, inetorgperson, nis and openssh-lpk schemas.
- **Encryption is required.** Port 389 accepts StartTLS and port 636 is LDAPS; plain, unencrypted connections are
  refused. On first start a self-signed certificate is made (valid ten years) for `localhost`, `openldap`,
  `LDAP_HOSTNAME` and anything in `LDAP_TLS_NAMES`. To use your own, put `tls.crt` and `tls.key` (and `ca.crt` if
  there is an intermediate chain) in the `ldap-tls` volume before the first start. Clients need to trust the
  certificate (for a self-signed one, copy it to them).
- Five volumes: `ldap-config` (the server's settings), `ldap-data` (the directory: back this up),
  `ldap-tls`, `ldap-handoff` and `ldap-backups`.

## The first-start hand-off

On the very first start the server makes its own administrator login for its configuration and writes it, with the
certificate, to `/handoff/bootstrap.json` in the `ldap-handoff` volume. Only the server and Dapper can read it.
Dapper picks it up during setup and then removes it. Nothing is baked into the image, and an existing volume is
never set up again.

## Testing the image

`scripts/e2e-ldap-image.sh` builds the image and checks all of the above, including that data survives a restart.
