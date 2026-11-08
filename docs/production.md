# Running Dapper against a live directory

A checklist for pointing Dapper at a directory people depend on.

## 1. Before you start

- **Back up the directory.** For OpenLDAP: `slapcat -n 1 > backup.ldif` (and `slapcat -n 0` for the config).
  Dapper can create, change, move and delete accounts and set passwords. Deleting is permanent.
- **Plan to start read-only.** Create the service account with *read* access first (see
  [service-account.md](service-account.md)). The People list, search and detail pages will work, and
  anything that writes will fail with a clear "service account doesn't have permission" message. Add write
  access once you're happy.
- **Practise on throwaway data.** Make a test container (for example `ou=dapper-test`) and try creating,
  suspending and deleting accounts there first.

## 2. Put HTTPS in front of Dapper

Dapper itself speaks plain HTTP. People type passwords into it, and so does the setup wizard, so run it
behind a reverse proxy that terminates TLS **before** you do setup.

Bind Dapper to the local machine only, in a `.env` file next to `docker-compose.yml`:

```
DAPPER_BIND=127.0.0.1
```

Caddy (the simplest; it gets certificates automatically):

```
dapper.example.org {
    reverse_proxy 127.0.0.1:8080
}
```

nginx: proxy to `http://127.0.0.1:8080` and include
`proxy_set_header X-Forwarded-Proto $scheme;` and `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`
so the session cookie is marked `Secure` and sign-in rate limiting sees real client addresses.

If you can't set up HTTPS yet, run the wizard through an SSH tunnel instead
(`ssh -L 8080:localhost:8080 yourserver`, then open <http://localhost:8080>).

## 3. Install and start

```sh
git clone https://github.com/koreesmith/dapper && cd dapper
git checkout v1.0.0
docker compose up -d --build
docker compose logs dapper        # note the setup code
```

## 4. Run the setup wizard

Open Dapper over HTTPS and follow the wizard:

- **Server address:** `ldaps://your-server:636`, or `ldap://your-server:389` with **StartTLS** ticked.
  Don't tick "don't verify the certificate" on a live server. If your server uses a private certificate
  authority, see "Trusting a private CA" below.
- **Service account:** a dedicated account for Dapper, not the directory's root account.
- **Where things live:** check the suggestions and correct them to match your tree.
  **Untick "Create these containers"** so Dapper never creates anything you didn't expect.
- **Suspended people:** create the container yourself (for example `ou=suspended`) and enter it. If you
  leave it empty, Dapper locks accounts in place, which needs OpenLDAP's `ppolicy` overlay.
- **Administrators:** choose **an existing group** whose members are people you trust to manage everyone.
  Avoid the "create" options on a live directory.

The Review step tests everything before anything is saved.

## 5. Check it works

1. Sign in as one of your administrators.
2. **People:** the list should show your real accounts. If someone's missing, they probably aren't a
   `person` (see below).
3. **Settings:** the status checklist should be all ticks.
4. Ask a colleague to sign in and check **My account**. By default people can edit their own email, phone
   numbers and address, and see (but not change) their name, title and department. Adjust this on the
   Settings page.
5. Then, with write access enabled and in your test container, create, edit, suspend and delete a test account.

## 6. Things to know about a live directory

- **Who shows up.** Dapper lists entries with `objectClass: person` (which `inetOrgPerson` and others
  inherit) under the people container.
- **Accounts used for Unix logins.** Accounts Dapper creates are plain `inetOrgPerson` entries. They don't
  get `posixAccount` attributes (`uidNumber`, `gidNumber`, `homeDirectory`), so they won't work for Linux
  logins. Editing existing accounts is fine. Creating POSIX accounts isn't supported yet.
- **Suspending moves the account** to the suspended container, so its DN changes. Dapper updates group
  membership in your groups container, but anything else that refers to the old DN (ACLs, other
  applications) won't follow. If that matters, use `ppolicy` locking instead.
- **Passwords** are set with the standard LDAP password-change operation, so your server's hashing and
  password policy apply. Dapper adds its own minimum length (default 8).
- **Audit trail.** Every administrative action is logged as an `audit` line:
  `docker compose logs dapper | grep audit`. Passwords are never logged.
- **Sessions** end after 30 idle minutes or 8 hours, adjustable under Settings.

## 7. Trusting a private CA

If your LDAP server's certificate is signed by your own authority, give Dapper the CA certificate. Create a
`docker-compose.override.yml` next to `docker-compose.yml` (it's ignored by git):

```yaml
services:
  dapper:
    volumes:
      - ./my-ca.pem:/etc/ssl/dapper-ca.pem:ro
    environment:
      SSL_CERT_FILE: /etc/ssl/dapper-ca.pem
```

then `docker compose up -d`.

## 8. Operating it

- **Keep the `dapper-data` volume.** It holds your settings (including the service account's password,
  stored owner-only). Back it up like you would any secret.
- **Upgrade:** `git pull && docker compose up -d --build`. Settings are kept.
- **Locked out** (for example the administrators group was emptied in the directory itself)? Remove the saved
  settings and run the wizard again: `docker compose down && docker volume rm dapper_dapper-data &&
  docker compose up -d`. A new setup code is printed.
- **Rotating the service account password:** change it in the directory, then on Settings enter the new
  password and save.
