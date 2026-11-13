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

## Setting it up

Open Dapper, enter the setup code, and choose **I need a new one**. Dapper finds the server, asks for your
organization and domain (the domain becomes the directory's name, so `example.org` gives `dc=example,dc=org`),
whether you use Linux accounts, and the first administrator. It lists everything it will create and waits for your
approval. At the end it shows two generated passwords **once**: the directory manager's (Dapper keeps this one so it
can work) and the configuration administrator's (Dapper never stores it; you enter it on the Server page). It also
shows the address, the certificate to trust and a starting `sssd.conf` for your Linux machines.

Choose **I already have an LDAP server** instead to connect Dapper to a directory you run yourself.

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

## Backups

The server backs itself up every day (`LDAP_BACKUP_INTERVAL_HOURS`, `0` turns the schedule off) and keeps the newest
14 (`LDAP_BACKUP_KEEP`). Each backup is one `.tar.gz` in the `ldap-backups` volume holding the server's
configuration and every directory as LDIF, plus a short manifest. Backups hold **every password hash**, so treat
copies like passwords.

On the Server page, **Backups** lists them, shows when the last one succeeded (and warns when it's overdue or
failed), and has **Back up now**. Dapper has no Docker access, so it asks by leaving a request file in the backups
volume, and the server makes the backup within a few seconds. Downloading a backup needs the server settings
unlocked as well as an administrator sign-in, and is recorded in Activity.

Keep a copy somewhere other than this machine. The volume lives on the same disk as the directory, so it won't
help if the disk is lost. To copy the newest backups out:

```sh
docker run --rm -v dapper_ldap-backups:/b -v "$PWD":/out alpine sh -c 'cp /b/dapper-ldap-*.tar.gz /out/'
```

(The volume name starts with your Compose project name, usually the folder name.)

## Restoring

Restoring puts the directory back exactly as it was when the backup was made; later changes are lost, and people
can't sign in while it runs. Dapper can't stop the server itself, so run these where Dapper is installed (the Server
page shows them with your chosen backup filled in):

```sh
docker compose -f docker-compose.yml -f docker-compose.server.yml stop openldap
docker compose -f docker-compose.yml -f docker-compose.server.yml run --rm openldap restore dapper-ldap-manual-20261109-120000.tar.gz
docker compose -f docker-compose.yml -f docker-compose.server.yml up -d
```

Before it replaces anything, restore saves the current state as a `pre-restore` backup, so it can be undone the same
way. The certificate is left alone. To bring a backup from another machine, copy the file into the `ldap-backups`
volume first.

## Upgrading the server image

1. Make a backup (Server → Backups → **Back up now**) and download or copy it somewhere safe.
2. Pull or rebuild, then recreate only the server: `docker compose -f docker-compose.yml -f docker-compose.server.yml up -d --build openldap`.
   The data, configuration and certificate live in volumes, so they carry over.
3. Check the Server page: the directory should answer and Dapper should still sign people in.

If something is wrong, put the old image back and restore the backup from step 1.

## Running it for real

- **Name and DNS.** Point a DNS name (for example `ldap.example.com`) at the machine and set `LDAP_HOSTNAME` to it
  before the first start, so the certificate carries that name. Changing it later means making a new certificate.
- **Firewall.** Open only the ports your Linux machines need to reach: 636 (LDAPS) and/or 389 (StartTLS). Dapper
  itself reaches the server over the private Compose network, so it doesn't need them. `LDAP_BIND` can limit which
  address they listen on.
- **Backups.** Copy backups off the machine regularly (see above); the volume shares the disk with the directory.
- **Dapper's own page.** Put Dapper behind an HTTPS reverse proxy as described in [production.md](production.md).
- **Your own certificate.** Put `tls.crt` and `tls.key` (and `ca.crt` for a chain) in the `ldap-tls` volume before the
  first start; clients then don't need to trust a self-signed certificate.

## If you lost the configuration password

The configuration administrator (`cn=config-admin,cn=config`) is shown once, when the server is set up or a migration is
switched, and Dapper never stores it. Anyone who can run commands inside the server container can set a new one: the
container's own local socket needs no password. Run these where Dapper is installed:

```sh
C="docker compose -f docker-compose.yml -f docker-compose.server.yml"
HASH=$($C exec -T openldap slappasswd -s 'a-new-long-password' | tr -d '\r\n')
$C exec -T openldap ldapmodify -Q -Y EXTERNAL -H "ldapi://%2Frun%2Fslapd%2Fldapi" <<EOF
dn: olcDatabase={0}config,cn=config
changetype: modify
replace: olcRootPW
olcRootPW: $HASH
EOF
```

Then unlock the Server page with `cn=config-admin,cn=config` and the new password. To use a different login name (for
example `cn=admin,cn=config`), run the same command with `replace: olcRootDN` and `olcRootDN: cn=admin,cn=config`.
Avoid putting the password in your shell history on a shared machine.
