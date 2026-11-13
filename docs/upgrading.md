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
- **Settings file:** optional keys may appear in `settings.yaml` after you use the new features: `ldap.caFile` (the
  certificate Dapper trusts), `ldap.publicHost` (the name of a Dapper-run server) and, during a migration,
  `previousLDAP` (the old server's connection, **including its saved password**, kept so the switch can be rolled
  back; **Finish the migration** removes it). Nothing is added if you don't use them, and 1.0.0 ignores them if you
  ever go back.
- **New pages:** Service accounts (always shown), and Migrate (shown while Dapper manages a server it doesn't run, or
  while a migration is unfinished).
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

### The footer shows the wrong version, or "Dapper dev"

Building with `docker compose up -d --build` shows the release you checked out (2.0.0 for this one) by default. If you
see "dev", the image was built some other way (a plain `docker build` without `--build-arg VERSION=…`), or from an
older checkout whose compose file didn't set it; build again with `docker compose up -d --build`, or set
`DAPPER_VERSION` in `.env`. Images pulled from `ghcr.io/koreesmith/dapper:<version>` already carry their version.

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
