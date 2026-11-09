#!/bin/bash
# Runs next to slapd: makes a backup on a schedule and whenever Dapper asks (by creating /backups/request),
# keeps the newest few, and writes /backups/status.json for Dapper to show.
OUT=${BACKUP_DIR:-/backups}
INTERVAL=${LDAP_BACKUP_INTERVAL_HOURS:-24}
KEEP=${LDAP_BACKUP_KEEP:-14}
STATE=$OUT/.loop-state
last_attempt=0; last_success=""; last_error=""
[ -f "$STATE" ] && . "$STATE"

has_database() { slapcat -n 0 -F /etc/ldap/slapd.d 2>/dev/null | grep -q '^olcSuffix:'; }

write_status() {
  local data free
  data=$(du -sb /var/lib/ldap 2>/dev/null | cut -f1)
  free=$(df -B1 --output=avail /var/lib/ldap 2>/dev/null | tail -1 | tr -d ' ')
  printf '{"lastAttempt":%s,"lastSuccess":"%s","lastError":"%s","intervalHours":%s,"keep":%s,"dataBytes":%s,"diskFreeBytes":%s,"image":"%s"}\n' \
    "${last_attempt:-0}" "$last_success" "${last_error//\"/\'}" "$INTERVAL" "$KEEP" "${data:-0}" "${free:-0}" "${DAPPER_LDAP_VERSION:-dev}" > "$OUT/status.json.tmp"
  mv "$OUT/status.json.tmp" "$OUT/status.json"
  printf 'last_attempt=%s\nlast_success=%q\nlast_error=%q\n' "$last_attempt" "$last_success" "$last_error" > "$STATE"
}

run() { # run <kind>
  local msg
  last_attempt=$(date +%s)
  if msg=$(/usr/local/bin/backup.sh "$1" 2>&1); then
    last_success=$(date -u +%Y-%m-%dT%H:%M:%SZ); last_error=""
    echo "dapper-ldap: backup made: $msg" >&2
    # keep the newest $KEEP scheduled and manual backups (saved-before-restore ones are never removed)
    ls -1t "$OUT"/dapper-ldap-scheduled-*.tar.gz "$OUT"/dapper-ldap-manual-*.tar.gz 2>/dev/null | tail -n +$((KEEP+1)) | xargs -r rm -f --
  else
    last_error=$msg
    echo "dapper-ldap: backup failed: $msg" >&2
  fi
  write_status
}

write_status
while true; do
  now=$(date +%s)
  if [ -e "$OUT/request" ]; then
    rm -f "$OUT/request"
    if has_database; then run manual; else last_error="There is nothing to back up yet: the directory hasn't been set up."; last_attempt=$now; write_status; fi
  elif [ "$INTERVAL" -gt 0 ] && has_database; then
    wait=$((INTERVAL*3600)); [ -n "$last_error" ] && wait=3600   # after a failure, try again within the hour
    [ $((now-last_attempt)) -ge "$wait" ] && run scheduled
  fi
  sleep 5
done
