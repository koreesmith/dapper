#!/usr/bin/env bash
# End-to-end test of first-run setup and the editable Settings page, against an
# UNCONFIGURED Dapper plus the demo directory:
#   docker compose -f docker-compose.yml -f docker-compose.demo.yml up -d --build
#   SETUP_CODE=<code from `docker compose logs dapper`> scripts/e2e-setup.sh
set -u
BASE=${BASE:-http://localhost:8080}
CODE_=${SETUP_CODE:?set SETUP_CODE to the code from the Dapper log}
PASS=0; FAIL=0
J=$(mktemp -d); trap 'rm -rf "$J"' EXIT

req() { # req <jar> <method> <path> [json] [setup-code]
  local jar=$1 m=$2 p=$3 d=${4:-} sc=${5:-}
  local out; out=$(curl -s -w '\n%{http_code}' -b "$J/$jar" -c "$J/$jar" -X "$m" \
    -H 'X-Requested-With: dapper' -H 'Content-Type: application/json' ${sc:+-H "X-Setup-Code: $sc"} ${d:+-d "$d"} "$BASE$p")
  CODE=${out##*$'\n'}; BODY=${out%$'\n'*}
}
jq_() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" <<<"$BODY"; }
check()  { if [ "$CODE" = "$2" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got $CODE, want $2): $BODY"; fi; }
expect() { if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got '$2', want '$3')"; fi; }

SETTINGS='{"ldap":{"url":"ldap://openldap:389","startTLS":false,"skipVerify":false,"bindDN":"cn=admin,dc=example,dc=org","bindPassword":"admin"},
"users":{"baseDN":"ou=people,dc=example,dc=org","loginAttributes":["uid","mail"],"suspendedBaseDN":"ou=suspended,dc=example,dc=org","objectClasses":["inetOrgPerson"],"rdnAttribute":"uid"},
"groups":{"baseDN":"ou=groups,dc=example,dc=org","objectClass":"groupOfNames","memberAttribute":"member"},
"admin":{"groupDN":"cn=dapper-admins,ou=groups,dc=example,dc=org"},"password":{"minLength":8},"session":{"idleMinutes":30,"maxHours":8}}'

req a GET /api/setup/status;                 expect "status says unconfigured" "$(jq_ "d['configured']")" False
req a POST /api/login '{"username":"alice","password":"alicepass"}'; check "sign-in refused before setup" 503
req a POST /api/setup/check "$SETTINGS";     check "setup needs a code" 401
req a POST /api/setup/check "$SETTINGS" wrong; check "wrong code rejected" 401
req a POST /api/setup/discover '{"ldap":{"url":"ldap://openldap:389","bindDN":"cn=admin,dc=example,dc=org","bindPassword":"nope"}}' "$CODE_"
expect "bad service password reported" "$(jq_ "d['ok']")" False
req a POST /api/setup/discover '{"ldap":{"url":"ldap://nowhere.invalid:389","bindDN":"x","bindPassword":"y"}}' "$CODE_"
expect "unreachable server reported" "$(jq_ "d['ok']")" False
req a POST /api/setup/discover '{"ldap":{"url":"ldap://openldap:389","bindDN":"cn=admin,dc=example,dc=org","bindPassword":"admin"}}' "$CODE_"
check "discover" 200
expect "suggests a people container" "$(jq_ "d['suggestions']['usersBaseDN']")" "ou=people,dc=example,dc=org"
req a POST /api/setup/check "$SETTINGS" "$CODE_"; expect "full check passes" "$(jq_ "d['ok']")" True

BAD=${SETTINGS/cn=dapper-admins,ou=groups/cn=no-such-group,ou=groups}
req a POST /api/setup/complete "{\"settings\":$BAD,\"createContainers\":false,\"admin\":{\"mode\":\"existing\"}}" "$CODE_"
check "missing admin group blocks setup" 400
req a POST /api/setup/complete "{\"settings\":$SETTINGS,\"createContainers\":false,\"admin\":{\"mode\":\"existing\"}}" "$CODE_"
check "complete setup" 200
req a GET /api/setup/status;                 expect "now configured" "$(jq_ "d['configured']")" True
req a POST /api/setup/check "$SETTINGS" "$CODE_"; check "setup closed once configured" 403

# --- the configured app, and the Settings page ---
req adm POST /api/login '{"username":"alice","password":"alicepass"}'; check "admin sign-in works" 200
req bob POST /api/login '{"username":"bob","password":"bobpass"}';     check "user sign-in works" 200
req bob GET /api/admin/settings;             check "settings are admin-only" 403
req adm GET /api/admin/settings;             check "read settings" 200
expect "password never returned" "$(jq_ "'bindPassword' in d['settings']['ldap']")" False
expect "password reported as saved" "$(jq_ "d['settings']['ldap']['bindPasswordSet']")" True
S=$BODY
mod() { python3 -c "import json,sys;d=json.loads(sys.argv[1])['settings'];exec(sys.argv[2]);print(json.dumps(d))" "$S" "$1"; }
req adm POST /api/admin/settings/check "$(mod "d['ldap']['url']='ldap://nowhere.invalid:389'")"
expect "test button reports a bad server" "$(jq_ "d['ok']")" False
req adm PUT /api/admin/settings "$(mod "d['ldap']['url']='ldap://nowhere.invalid:389'")"
check "unreachable settings aren't saved" 400
# A group that doesn't include the person saving: switching to it would lock them out.
req adm POST /api/admin/groups '{"name":"e2e-not-alice"}'
req adm POST "/api/admin/group/members?dn=cn=e2e-not-alice,ou=groups,dc=example,dc=org" '{"member":"uid=bob,ou=people,dc=example,dc=org"}'
req adm PUT /api/admin/settings "$(mod "d['admin']['groupDN']='cn=e2e-not-alice,ou=groups,dc=example,dc=org'")"
check "can't save settings that lock you out" 400
req adm DELETE "/api/admin/group?dn=cn=e2e-not-alice,ou=groups,dc=example,dc=org"
req adm PUT /api/admin/settings "$(mod "d['password']['minLength']=12; d['session']['idleMinutes']=45")"
check "save valid change" 200
req adm GET /api/admin/settings;             expect "change persisted" "$(jq_ "d['settings']['password']['minLength']")" 12
req adm POST "/api/admin/user/password?dn=uid=bob,ou=people,dc=example,dc=org" '{"password":"elevenchars"}'
check "new minimum length enforced live" 400
req adm PUT /api/admin/settings "$(mod "d['password']['minLength']=8")"; check "restore" 200

echo; echo "passed $PASS, failed $FAIL"
[ "$FAIL" = 0 ]
