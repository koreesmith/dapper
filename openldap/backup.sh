#!/bin/bash
# One backup: the server's configuration and every directory database, as LDIF, in one archive on /backups.
# Usage: backup.sh [scheduled|manual|pre-restore]
set -euo pipefail
KIND=${1:-manual}
OUT=${BACKUP_DIR:-/backups}
CONF=/etc/ldap/slapd.d
umask 0037
TS=$(date -u +%Y%m%d-%H%M%S)
WORK=$(mktemp -d); trap 'rm -rf "$WORK"' EXIT

slapcat -n 0 -F "$CONF" -l "$WORK/config.ldif"
: > "$WORK/manifest"
{
  echo "created=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "kind=$KIND"
  echo "image=${DAPPER_LDAP_VERSION:-dev}"
  echo "slapd=$(slapd -VV 2>&1 | sed -n 's/.*slapd \([0-9.]*[^ ]*\).*/\1/p' | head -1)"
} >> "$WORK/manifest"
n=0
while IFS= read -r suffix; do
  [ -z "$suffix" ] && continue
  n=$((n+1))
  slapcat -F "$CONF" -b "$suffix" -l "$WORK/data-$n.ldif"
  echo "database$n=$suffix" >> "$WORK/manifest"
  echo "entries$n=$(grep -c '^dn:' "$WORK/data-$n.ldif" || true)" >> "$WORK/manifest"
done < <(sed -n 's/^olcSuffix: //p' "$WORK/config.ldif")

NAME="dapper-ldap-$KIND-$TS.tar.gz"
tar -czf "$OUT/.$NAME.partial" -C "$WORK" .
mv "$OUT/.$NAME.partial" "$OUT/$NAME"   # only ever listed once complete
echo "$NAME"
