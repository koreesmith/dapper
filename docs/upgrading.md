# Upgrading and operating Dapper

## Upgrading

```sh
git pull                      # or pull the new image
docker compose up -d --build  # keeps your settings: they live in the dapper-data volume
```

or, with the published image, `docker pull ghcr.io/koreesmith/dapper:<version>` and restart the container with the
same `/data` volume. Your settings (`settings.yaml`, `policy.yaml`) are read by the new version as they are. Check
the version in the page footer, or with `dapper -version`.

Before upgrading, copy the data volume somewhere safe (it holds your settings, including the service account's
password, and saved server-configuration backups):

```sh
docker run --rm -v dapper-data:/data -v "$PWD":/backup alpine tar czf /backup/dapper-data.tgz -C /data .
```

Release notes are in [CHANGELOG.md](../CHANGELOG.md). Anything that needs your attention when upgrading is called
out there.

## From 1.0.0 to 2.0.0

Nothing needs to change. A 1.0.0 install keeps working with the same `docker-compose.yml`, the same data volume
and the same settings.

- **Back up the data volume first**, as above.
- **Settings file:** two optional keys may appear in `settings.yaml` after you use the new features (`ldap.caFile`,
  the certificate Dapper trusts, and `ldap.publicHost`, the name of a Dapper-run server). Nothing is added if you
  don't use them, and 1.0.0 ignores them if you ever go back.
- **Running your own server?** You can ignore everything about the Dapper-run server. The new **Trusted
  certificate** section on the Settings page is useful if your server uses its own certificate, because it lets you
  turn "skip certificate checks" off.
- **Want Dapper to run a directory server?** That is a separate setup (a new server, not a conversion of yours);
  see [dapper-server.md](dapper-server.md). Moving an existing directory into it is not part of 2.0.0.
- **Behaviour changes you may notice:** people with a picture show it on the People page; an ended session sends you
  to the sign-in screen; searching for text that mixes letters and digits no longer matches phone numbers; the refint
  overlay writes one attribute name per value (older servers accept both).
- **The Dapper-run server image** upgrades separately; see "Upgrading the server image" in
  [dapper-server.md](dapper-server.md).

### The footer says "Dapper dev"

That is what an image shows when it was built without a version. Building with `docker compose up -d --build`
doesn't know which release you checked out, so set `DAPPER_VERSION` in `.env` to the release you checked out (for example `2.0.0`) and
build again. Images pulled from `ghcr.io/koreesmith/dapper:<version>` already carry their version.

## Things to know when running it

- **One instance.** Sessions are kept in memory, so run a single copy. Restarting Dapper signs everyone out; nothing
  else is lost.
- **Large directories.** The People list loads up to 5,000 accounts per search and filters them in memory; the
  list warns you if your directory is bigger. Searches use paged results, so the server's own search size limit
  (500 by default on OpenLDAP) does not cut results short.
- **Units.** Only the people container and the units inside it are managed. A unit can be renamed or removed only while
  it is empty, because OpenLDAP can't rename a unit that has entries in it. Moving someone changes their name in the
  directory; Dapper updates their group memberships and ends their sessions, but other systems that store the old name
  need updating.
- **Pictures** are stored in `jpegPhoto`, re-encoded as JPEG, and limited to 2 MB (the browser shrinks them first).
  Whether people can change their own is controlled on the Settings page like any other detail.
- **Time.** Session timeouts and password expiry dates depend on the clock; keep the host's time correct.
- **Logs.** `docker compose logs dapper` shows startup, sign-in problems and every administrative action
  (`audit` lines). The same actions appear on the **Activity** page.
- **Recovering access.** If nobody can sign in as an administrator (for example the administrators group was emptied
  in the directory itself), remove the saved settings and run the setup wizard again:
  `docker compose down && docker volume rm dapper_dapper-data && docker compose up -d`, then read the new setup code
  from the log.
- **Rotating the service account's password:** change it in the directory, then enter the new one on the Settings page.
