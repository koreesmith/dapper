#!/bin/bash
# Restores a backup. Run it as a one-off container with the directory server STOPPED:
#   docker compose ... stop openldap
#   docker compose ... run --rm openldap restore dapper-ldap-manual-20261109-120000.tar.gz
#   docker compose ... up -d
# The current state is saved first as a "pre-restore" backup. Certificates are left as they are.
set -euo pipefail
OUT=${BACKUP_DIR:-/backups}
CONF=/etc/ldap/slapd.d
FILE=${1:-}
[ -n "$FILE" ] || { echo "Usage: restore <backup file name>" >&2; exit 2; }
case "$FILE" in */*|*..*) echo "Give the backup's file name only, without a path." >&2; exit 2;; esac
[ -f "$OUT/$FILE" ] || { echo "No such backup: $OUT/$FILE" >&2; ls -1 "$OUT" | grep '^dapper-ldap-' >&2 || true; exit 1; }

WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT
tar -xzf "$OUT/$FILE" -C "$WORK" 2>/dev/null || { echo "$FILE isn't a Dapper directory backup (it can't be opened as one). Nothing was changed." >&2; exit 1; }
[ -f "$WORK/config.ldif" ] && [ -f "$WORK/manifest" ] || { echo "$FILE isn't a Dapper directory backup. Nothing was changed." >&2; exit 1; }

if [ -f "$CONF/cn=config.ldif" ]; then
  echo "Saving the current state first..."
  /usr/local/bin/backup.sh pre-restore
fi

echo "Restoring $FILE ..."
rm -rf "${CONF:?}"/* /var/lib/ldap/*
slapadd -n 0 -F "$CONF" -l "$WORK/config.ldif"
n=0
while IFS='=' read -r key suffix; do
  case "$key" in database*)
    n=${key#database}
    slapadd -F "$CONF" -b "$suffix" -l "$WORK/data-$n.ldif"
    echo "  restored $suffix"
  ;; esac
done < "$WORK/manifest"
echo "Done. Start the server again."
