#!/usr/bin/env bash
# Service accounts (read-only accounts for applications) against the demo OpenLDAP, whose access rules only let the
# directory manager read people: so a new account can sign in but see nothing until the read rule is added.
# Needs a configured Dapper + the demo directory, and docker access to the demo container. Leaves things as found.
set -u
BASE=${BASE:-http://localhost:8080}
LDAPC=${LDAP_CONTAINER:-dapper-openldap-1}
SUF=dc=example,dc=org
PASS=0; FAIL=0
J=$(mktemp -d); trap 'rm -rf "$J"' EXIT
req() { local jar=$1 m=$2 p=$3 d=${4:-}
  local out; out=$(curl -s -w '\n%{http_code}' -b "$J/$jar" -c "$J/$jar" -X "$m" -H 'X-Requested-With: dapper' -H 'Content-Type: application/json' ${d:+-d "$d"} "$BASE$p")
  CODE=${out##*$'\n'}; BODY=${out%$'\n'*}; }
jq_() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" <<<"$BODY"; }
expect() { if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got '$2', want '$3') ${BODY:0:300}"; fi; }
dwho() { docker exec "$LDAPC" ldapwhoami -x -H ldap://localhost -D "$1" -w "$2" 2>&1 | head -1; }
q() { python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))" "$1"; }
BOX="ou=services,$SUF"; DN="cn=e2esvc,$BOX"

req adm POST /api/login '{"username":"alice","password":"alicepass"}'; expect "admin signs in" "$CODE" 200
req bob POST /api/login '{"username":"bob","password":"bobpass"}'
req bob GET /api/admin/service-accounts; expect "service accounts are admin-only" "$CODE" 403
# leftovers from an interrupted run
req adm GET /api/admin/service-accounts; [ "$(jq_ "any(a['name']=='e2esvc' for a in d['accounts'])")" = True ] && req adm DELETE "/api/admin/service-account?dn=$(q $DN)" >/dev/null
req adm POST /api/admin/server/preview '{"kind":"x"}' >/dev/null

req adm POST /api/admin/service-accounts '{"name":"e2esvc","description":"test"}'; expect "creating needs approval" "$CODE" 400
req adm POST /api/admin/service-accounts '{"name":"Bad Name","confirmed":true}'; expect "a bad name is refused" "$CODE" 400
req adm POST /api/admin/service-accounts '{"name":"e2esvc","password":"short","confirmed":true}'; expect "a short password is refused" "$CODE" 400
req adm POST /api/admin/service-accounts '{"name":"e2esvc","description":"e2e test account","confirmed":true}'; expect "create a service account" "$CODE" 200
PW=$(jq_ "d['password']")
expect "a strong password was generated and returned once" "$(jq_ "len(d['password'])")" 24
expect "the account signs in" "$(jq_ "d['test']['signedIn']")" True
expect "but the demo's rules show it no people" "$(jq_ "d['test']['canRead'], d['test']['seesHashes']")" "(0, False)"
req adm POST /api/admin/service-accounts '{"name":"e2esvc","confirmed":true}'; expect "a duplicate name is refused" "$CODE" 400
req adm GET /api/admin/service-accounts; expect "the container now exists and the account is listed" "$(jq_ "d['containerExists'] and [a['name'] for a in d['accounts']]==['e2esvc'] and d['accounts'][0]['description']=='e2e test account'")" True
expect "it names the container" "$(jq_ "d['container']")" "$BOX"
expect "the server really accepts the password" "$(dwho "$DN" "$PW")" "dn:$DN"
expect "it is not a person (not in the People list)" "$(req adm GET '/api/admin/users?q=e2esvc'; jq_ "d['total']")" 0

# it can't change anything
docker exec -i "$LDAPC" ldapmodify -x -H ldap://localhost -D "$DN" -w "$PW" >/dev/null 2>&1 <<EOF
dn: uid=bob,ou=people,$SUF
changetype: modify
replace: title
title: hacked
EOF
req adm GET "/api/admin/user?dn=$(q uid=bob,ou=people,$SUF)"; expect "it can't write to people" "$(jq_ "'hacked' in str(d)")" False

# the read rule, through the Server page's own preview/apply, needs the unlocked settings
req adm POST /api/admin/server/preview '{"kind":"acl.service-read"}'; expect "changing access rules needs the settings unlocked" "$CODE" 409
req adm POST /api/admin/overlays/unlock '{"dn":"cn=admin,cn=config","password":"config"}'; expect "unlock the server settings" "$CODE" 200
req adm GET /api/admin/server/config; ACL=$(jq_ "__import__('json').dumps(d['acl'])")
req adm POST /api/admin/server/preview '{"kind":"acl.service-read"}'; expect "the read rule is previewed" "$CODE" 200
expect "in plain words, naming the container" "$(jq_ "'ou=services' in ' '.join(d['summary'])")" True
expect "as one added clause in the catch-all rule" "$(jq_ "'dn.children=\\\"$BOX\\\" read' in d['ldif']")" True
req adm POST /api/admin/server/apply '{"kind":"acl.service-read"}'; expect "applying needs approval" "$CODE" 400
req adm POST /api/admin/server/apply '{"kind":"acl.service-read","confirmed":true}'; expect "apply the read rule" "$CODE" 200
req adm POST /api/admin/service-accounts/test "{\"dn\":\"$DN\",\"password\":\"$PW\"}"
expect "now the account reads people but still can't see password hashes" "$(jq_ "d['test']['canRead']>0, d['test']['seesHashes']")" "(True, False)"
req adm POST /api/admin/server/preview '{"kind":"acl.service-read"}'; expect "asking again says it is already allowed" "$CODE" 400
req adm POST /api/admin/service-accounts/test "{\"dn\":\"$DN\",\"password\":\"wrong-password-here\"}"; expect "a wrong password is reported, not crashed" "$(jq_ "d['test']['signedIn']")" False
req adm POST /api/admin/service-accounts/test "{\"dn\":\"uid=bob,ou=people,$SUF\",\"password\":\"bobpass\"}"; expect "the test only works on service accounts" "$CODE" 400

# reset the password
req adm POST /api/admin/service-accounts/password "{\"dn\":\"$DN\"}"; expect "a new password needs approval" "$CODE" 400
req adm POST /api/admin/service-accounts/password "{\"dn\":\"$DN\",\"confirmed\":true}"; expect "reset the password" "$CODE" 200
PW2=$(jq_ "d['password']")
expect "the new password is different" "$([ "$PW" != "$PW2" ] && echo yes)" yes
expect "the old password stops working" "$(dwho "$DN" "$PW" | grep -c 'Invalid credentials')" 1
expect "the new one works" "$(dwho "$DN" "$PW2")" "dn:$DN"
req adm POST /api/admin/service-accounts/password "{\"dn\":\"uid=bob,ou=people,$SUF\",\"confirmed\":true}"; expect "a person's password can't be reset from here" "$CODE" 400

# deleting
req adm DELETE "/api/admin/service-account?dn=$(q uid=bob,ou=people,$SUF)"; expect "a person can't be deleted from here" "$CODE" 400
req adm DELETE "/api/admin/service-account?dn=$(q $BOX)"; expect "the container can't be deleted from here" "$CODE" 400
req adm DELETE "/api/admin/service-account?dn=$(q $DN)"; expect "delete the service account" "$CODE" 200
req adm GET /api/admin/service-accounts; expect "it is gone" "$(jq_ "d['accounts']")" "[]"
expect "and can't sign in any more" "$(dwho "$DN" "$PW2" | grep -c 'Invalid credentials')" 1

# put the demo's access rules back as they were, and remove the empty container
req adm POST /api/admin/server/apply "{\"kind\":\"acl.set\",\"rules\":$ACL,\"confirmed\":true}"; expect "the original access rules are restored" "$CODE" 200
docker exec "$LDAPC" ldapdelete -x -H ldap://localhost -D "cn=admin,$SUF" -w admin "$BOX" >/dev/null 2>&1
req adm POST /api/admin/overlays/lock '{}' >/dev/null
echo; echo "passed $PASS, failed $FAIL"; [ "$FAIL" = 0 ]
