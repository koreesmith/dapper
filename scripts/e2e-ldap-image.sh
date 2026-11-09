#!/usr/bin/env bash
# Smoke test for the Dapper OpenLDAP image (openldap/). Builds it, starts it on empty volumes and checks:
# first-start hand-off, TLS required, plain binds refused, overlay modules and schemas present, a
# database can be added through cn=config, and a restart keeps the data and the hand-off untouched.
# Usage: scripts/e2e-ldap-image.sh [image]   (builds dapper-ldap:test when no image is given)
set -u
IMG=${1:-}
if [ -z "$IMG" ]; then IMG=dapper-ldap:test; docker build -q -t "$IMG" "$(dirname "$0")/../openldap" >/dev/null || exit 1; fi
P=dapperldaptest$RANDOM; NET=$P-net; SRV=$P-srv
PASS=0; FAIL=0
cleanup() { docker rm -f "$SRV" >/dev/null 2>&1; docker network rm "$NET" >/dev/null 2>&1; docker volume rm "$P-c" "$P-d" "$P-t" "$P-h" >/dev/null 2>&1; }
trap cleanup EXIT
ok() { if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got '$2', want '$3')"; fi; }
has() { if grep -q -- "$3" <<<"$2"; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (no '$3' in: $(head -c 300 <<<"$2"))"; fi; }

docker network create "$NET" >/dev/null
start() {
  docker run -d --name "$SRV" --network "$NET" --network-alias ldap.test -e LDAP_HOSTNAME=ldap.test \
    -v "$P-c":/etc/ldap/slapd.d -v "$P-d":/var/lib/ldap -v "$P-t":/etc/ldap/tls -v "$P-h":/handoff "$IMG" >/dev/null
  for _ in $(seq 1 40); do [ "$(docker inspect -f '{{.State.Health.Status}}' "$SRV")" = healthy ] && return 0; sleep 1; done
  return 1
}
# cl <shell commands>: run LDAP tools in a client container that can read the hand-off file.
cl() {
  docker run --rm --network "$NET" --user 0 -v "$P-h":/h:ro --entrypoint bash "$IMG" -c '
    PW=$(sed -n "s/.*\"configPassword\": \"\(.*\)\",/\1/p" /h/bootstrap.json)
    sed -n "s/.*\"certificate\": \"\(.*\)\"/\1/p" /h/bootstrap.json | sed "s/\\\\n/\n/g" > /tmp/ca.pem
    export LDAPTLS_CACERT=/tmp/ca.pem; CFG="-x -D cn=config-admin,cn=config -w $PW"
    '"$1" 2>&1
}

start; ok "server starts and is healthy" "$?" 0
FILE=$(docker run --rm -v "$P-h":/h:ro --entrypoint sh "$IMG" -c 'ls -l /h/bootstrap.json | cut -c1-10,17-')
has "hand-off is readable by the owner and Dapper's group only" "$FILE" "^-rw-r-----"
has "hand-off names the configuration login" "$(cl 'cat /h/bootstrap.json')" 'cn=config-admin,cn=config'

has "plain bind is refused" "$(cl 'ldapwhoami $CFG -H ldap://ldap.test')" "onfidentiality required"
has "anonymous plain search is refused" "$(cl 'ldapsearch -x -H ldap://ldap.test -b "" -s base')" "onfidentiality required"
has "StartTLS and the configuration login work" "$(cl 'ldapwhoami $CFG -ZZ -H ldap://ldap.test')" "cn=config-admin,cn=config"
has "LDAPS works" "$(cl 'ldapwhoami $CFG -H ldaps://ldap.test')" "cn=config-admin,cn=config"
has "wrong password is refused" "$(cl 'ldapwhoami -x -D cn=config-admin,cn=config -w nope -ZZ -H ldap://ldap.test')" "nvalid credentials"
MODS=$(cl 'ldapsearch $CFG -LLL -ZZ -H ldap://ldap.test -b "cn=module{0},cn=config" olcModuleLoad')
for m in memberof refint ppolicy unique auditlog lastbind syncprov back_mdb; do has "module $m is loaded" "$MODS" "$m"; done
SCH=$(cl 'ldapsearch $CFG -LLL -ZZ -H ldap://ldap.test -b "cn=schema,cn=config" -s one dn')
for s in core cosine inetorgperson nis openssh-lpk; do has "schema $s is present" "$SCH" "$s"; done

# Adding the directory database through cn=config is how Dapper's guided setup will create it.
cl 'ldapadd $CFG -ZZ -H ldap://ldap.test <<EOF
dn: olcDatabase={1}mdb,cn=config
objectClass: olcDatabaseConfig
objectClass: olcMdbConfig
olcDatabase: {1}mdb
olcDbDirectory: /var/lib/ldap
olcSuffix: dc=test,dc=lan
olcRootDN: cn=admin,dc=test,dc=lan
olcRootPW: adminpw
olcDbIndex: objectClass eq
EOF' >/dev/null
ADD=$(cl 'ldapadd -x -ZZ -H ldap://ldap.test -D cn=admin,dc=test,dc=lan -w adminpw <<EOF
dn: dc=test,dc=lan
objectClass: dcObject
objectClass: organization
dc: test
o: Test
EOF')
has "a directory can be added and filled" "$ADD" "adding new entry"

CERT1=$(docker run --rm -v "$P-t":/t:ro --entrypoint sh "$IMG" -c 'sha256sum /t/tls.crt')
HAND1=$(docker run --rm -v "$P-h":/h:ro --entrypoint sh "$IMG" -c 'sha256sum /h/bootstrap.json')
docker restart "$SRV" >/dev/null
for _ in $(seq 1 40); do [ "$(docker inspect -f '{{.State.Health.Status}}' "$SRV")" = healthy ] && break; sleep 1; done
has "data survives a restart" "$(cl 'ldapsearch -x -ZZ -H ldap://ldap.test -D cn=admin,dc=test,dc=lan -w adminpw -b dc=test,dc=lan -s base dn')" "dn: dc=test,dc=lan"
ok "certificate is not remade on restart" "$(docker run --rm -v "$P-t":/t:ro --entrypoint sh "$IMG" -c 'sha256sum /t/tls.crt')" "$CERT1"
ok "hand-off is not rewritten on restart" "$(docker run --rm -v "$P-h":/h:ro --entrypoint sh "$IMG" -c 'sha256sum /h/bootstrap.json')" "$HAND1"
has "certificate has the server's name" "$(cl 'openssl x509 -in /tmp/ca.pem -noout -ext subjectAltName')" "ldap.test"

echo; echo "passed $PASS, failed $FAIL"; [ "$FAIL" = 0 ]
