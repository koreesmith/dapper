#!/bin/bash
# Starts slapd. On the very first start (empty configuration volume) it builds a configuration,
# makes a certificate if none was supplied, and writes the generated logins to /handoff for Dapper.
set -euo pipefail

CONF=/etc/ldap/slapd.d
TLS=/etc/ldap/tls
HANDOFF=/handoff
HOST=${LDAP_HOSTNAME:-localhost}
NAMES=${LDAP_TLS_NAMES:-}     # extra certificate names, comma separated

# "restore <backup>" is run as a one-off container, with the server stopped.
if [ "${1:-}" = restore ]; then shift; exec /usr/local/bin/restore.sh "$@"; fi

log() { echo "dapper-ldap: $*" >&2; }
secret() { openssl rand -base64 32 | tr -d '/+=\n' | cut -c1-32; }

make_certificate() {
  local san="DNS:localhost,DNS:openldap,IP:127.0.0.1" n
  for n in "$HOST" ${NAMES//,/ }; do
    [ -z "$n" ] && continue
    if [[ "$n" =~ ^[0-9.]+$ || "$n" == *:* ]]; then san+=",IP:$n"; else san+=",DNS:$n"; fi
  done
  log "making a self-signed certificate for: ${san//DNS:/}"
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -days 3650 \
    -subj "/O=Dapper/CN=$HOST" -addext "subjectAltName=$san" \
    -keyout "$TLS/tls.key" -out "$TLS/tls.crt" 2>/dev/null
}

if [ ! -f "$CONF/cn=config.ldif" ]; then
  log "first start: building the configuration"
  [ -f "$TLS/tls.crt" ] && [ -f "$TLS/tls.key" ] || make_certificate
  chmod 0600 "$TLS/tls.key"

  # With no CA file the line is dropped (a blank line would end the entry).
  tlsca="/@TLSCA@/d"
  [ -f "$TLS/ca.crt" ] && tlsca="s|@TLSCA@|olcTLSCACertificateFile: $TLS/ca.crt|"
  configdn="cn=config-admin,cn=config"
  configpw=$(secret)
  sed -e "$tlsca" \
      -e "s|@CONFIGDN@|$configdn|" \
      -e "s|@CONFIGPW@|$(slappasswd -s "$configpw" | sed 's/[|&\\]/\\&/g')|" \
      -e "s|@UID@|$(id -u)|" -e "s|@GID@|$(id -g)|" \
      /usr/share/dapper-ldap/config.ldif.template > /tmp/config.ldif
  slapadd -n 0 -F "$CONF" -l /tmp/config.ldif
  rm -f /tmp/config.ldif

  # What Dapper needs to take the server over: the configuration login and the certificate to trust.
  cert=$( [ -f "$TLS/ca.crt" ] && cat "$TLS/ca.crt" || cat "$TLS/tls.crt" )
  umask 0037
  {
    printf '{\n  "configDN": "%s",\n  "configPassword": "%s",\n' "$configdn" "$configpw"
    printf '  "host": "%s",\n  "certificate": "%s"\n}\n' "$HOST" "$(printf '%s' "$cert" | sed ':a;N;$!ba;s/\n/\\n/g')"
  } > "$HANDOFF/bootstrap.json.tmp"
  mv "$HANDOFF/bootstrap.json.tmp" "$HANDOFF/bootstrap.json"
  log "first start done; the logins are in $HANDOFF/bootstrap.json for Dapper"
fi

/usr/local/bin/backup-loop.sh &
exec slapd -h "ldap:/// ldaps:/// ldapi://%2Frun%2Fslapd%2Fldapi" -F "$CONF" -d "${LDAP_LOG_LEVEL:-stats}"
