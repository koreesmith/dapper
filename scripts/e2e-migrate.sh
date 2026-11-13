#!/usr/bin/env bash
# Migration of an existing directory into a Dapper-run server, end to end through Dapper's API, with two real
# servers. Needs an UNCONFIGURED Dapper started with the test stack:
#   export DAPPER_SETUP_CODE=ci-setup-code LDAP_HOSTNAME=ldap.test
#   docker compose -f docker-compose.yml -f docker-compose.server.yml -f docker-compose.migrate-test.yml up -d --build
#   SETUP_CODE=ci-setup-code scripts/e2e-migrate.sh [base-url]
set -u
BASE=${1:-http://localhost:8080}
: "${SETUP_CODE:?set SETUP_CODE to the code from the Dapper log}"
PASS=0; FAIL=0
J=$(mktemp -d); trap 'rm -rf "$J"' EXIT
req() { # req <jar> <method> <path> [json]
  local out; out=$(curl -s -w '\n%{http_code}' -b "$J/$1" -c "$J/$1" -X "$2" -H 'X-Requested-With: dapper' \
    -H "X-Setup-Code: $SETUP_CODE" -H 'Content-Type: application/json' ${4:+-d "$4"} "$BASE$3")
  CODE=${out##*$'\n'}; BODY=${out%$'\n'*}
}
jq_() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" <<<"$BODY"; }
expect() { if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got '$2', want '$3') ${BODY:0:300}"; fi; }
SRC='{"sourceDN":"cn=admin,dc=example,dc=org","sourcePassword":"admin","keepManager":true'
wait_job() { for _ in $(seq 1 120); do req adm GET /api/admin/migration; [ "$(jq_ "d['job']['running']")" = False ] && return; sleep 1; done; }

SETTINGS='{"ldap":{"url":"ldap://source-ldap:389","startTLS":false,"skipVerify":false,"bindDN":"cn=admin,dc=example,dc=org","bindPassword":"admin"},
"users":{"baseDN":"ou=people,dc=example,dc=org","loginAttributes":["uid","mail"],"suspendedBaseDN":"ou=suspended,dc=example,dc=org","objectClasses":["inetOrgPerson"],"rdnAttribute":"uid"},
"groups":{"baseDN":"ou=groups,dc=example,dc=org","objectClass":"groupOfNames","memberAttribute":"member"},
"admin":{"groupDN":"cn=dapper-admins,ou=groups,dc=example,dc=org"},"password":{"minLength":8},"session":{"idleMinutes":30,"maxHours":8}}'

# --- an existing install, configured against the old server ---
req a POST /api/setup/complete "{\"settings\":$SETTINGS,\"createContainers\":false,\"admin\":{\"mode\":\"existing\"}}"; expect "Dapper is set up against the old server" "$CODE" 200
req adm POST /api/login '{"username":"alice","password":"alicepass"}'; expect "the administrator signs in" "$CODE" 200
# give someone a properly hashed password (the old server hashes it), so a real hash has to carry over
req adm POST "/api/admin/user/password?dn=uid%3Dbob%2Cou%3Dpeople%2Cdc%3Dexample%2Cdc%3Dorg" '{"password":"migrated-secret-1"}'; expect "bob gets a hashed password on the old server" "$CODE" 200
req adm POST /api/admin/overlays/unlock '{"dn":"cn=admin,cn=config","password":"config"}'; expect "the old server's settings unlock" "$CODE" 200

req adm GET /api/admin/migration; expect "the new server is found" "$(jq_ "d['newServer']['found'], d['newServer']['reachable']")" "(True, True)"
expect "and Dapper isn't running the old one" "$(jq_ "d['dapperRun']")" False
req bob POST /api/login '{"username":"bob","password":"migrated-secret-1"}'
req bob GET /api/admin/migration; expect "migration is admin-only" "$CODE" 403

# --- the check ---
req adm POST /api/admin/migration/check '{"sourceDN":"cn=admin,dc=example,dc=org","sourcePassword":"wrong"}'; expect "a wrong manager password is explained" "$CODE" 400
req adm POST /api/admin/migration/check "$SRC}"; expect "the check runs" "$CODE" 200
N=$(jq_ "d['entries']")
expect "it names the directory" "$(jq_ "d['suffix']")" "dc=example,dc=org"
expect "it counts people, groups and units" "$(jq_ "d['kinds']['people']>30 and d['kinds']['groups']==2 and d['kinds']['units']>=2")" True
expect "it sees the hashed password and the plain ones" "$(jq_ "any(h['scheme']=='SSHA' and h['supported'] for h in d['hashes']) and d['plainText']>0")" True
expect "there are no blockers" "$(jq_ "d['blockers']")" "[]"
expect "with the server unlocked, memberOf and refint will be carried" "$(jq_ "'memberof' in d['carryOverlays'] and 'refint' in d['carryOverlays']")" True
expect "it says what isn't carried over" "$(jq_ "len(d['notCarried'])>=2")" True

# --- the copy ---
req adm POST /api/admin/migration/copy "$SRC}"; expect "copying needs approval" "$CODE" 400
req adm POST /api/admin/migration/copy "$SRC,\"confirmed\":true}"; expect "the copy starts" "$CODE" 202
req adm POST /api/admin/migration/copy "$SRC,\"confirmed\":true}"; expect "a second copy can't start while one runs (or finished already)" "$([ "$CODE" = 409 ] || [ "$CODE" = 202 ] && echo fine)" fine
wait_job
req adm GET /api/admin/migration
expect "the copy finishes without error" "$(jq_ "d['job']['error'] if 'error' in d['job'] else ''")" ""
expect "every entry was copied" "$(jq_ "d['job']['result']['failed'], d['job']['result']['sourceEntries']==$N")" "(0, True)"
expect "and checked to be identical to the old one" "$(jq_ "d['job']['result']['verified'], d['job']['result']['mismatched']")" "(True, 0)"
expect "the overlays were turned on at the new server" "$(jq_ "sorted(d['job']['result']['carriedOverlays'])")" "['memberof', 'refint']"
expect "Dapper says it is ready to switch" "$(jq_ "d['readyToSwitch']")" True
expect "the old directory manager is kept on the new server" "$(jq_ "d['job']['result']['managerKept'], d['job']['result']['managerDN']")" "(True, 'cn=admin,dc=example,dc=org')"
if [ -n "${COMPOSE:-}" ]; then
  expect "the OLD manager name and password sign in to the NEW server" "$($COMPOSE exec -T -e LDAPTLS_REQCERT=never openldap ldapwhoami -x -ZZ -H ldap://localhost -D cn=admin,dc=example,dc=org -w admin 2>&1 | tr -d '\r')" "dn:cn=admin,dc=example,dc=org"
fi

# --- run it again: nothing new, then one change on the old server is picked up ---
req adm PATCH "/api/admin/user?dn=uid%3Dbob%2Cou%3Dpeople%2Cdc%3Dexample%2Cdc%3Dorg" '{"attrs":{"title":["Migrated"]}}'; expect "someone is changed on the old server" "$CODE" 200
req adm POST /api/admin/migration/copy "$SRC,\"confirmed\":true}"; expect "the copy runs again" "$CODE" 202
wait_job; req adm GET /api/admin/migration
expect "the repeat adds nothing and updates just the changed person" "$(jq_ "d['job']['result']['added'], d['job']['result']['updated'], d['job']['result']['verified']")" "(0, 1, True)"

# --- switch ---
req adm POST /api/admin/migration/switch '{}'; expect "switching needs approval" "$CODE" 400
req adm POST /api/admin/migration/switch '{"confirmed":true}'; expect "Dapper switches to the new server" "$CODE" 200
expect "no new manager password is made or shown when the old one is kept" "$(jq_ "d['managerKept'], 'managerPassword' in d, d['managerDN']")" "(True, False, 'cn=admin,dc=example,dc=org')"
expect "the configuration password is shown once" "$(jq_ "len(d['configPassword'])>=20")" True
expect "and there is a checklist" "$(jq_ "len(d['checklist'])>=8")" True
req adm GET /api/admin/users; expect "old sessions end at the switch" "$CODE" 401
req n POST /api/login '{"username":"alice","password":"alicepass"}'; expect "alice signs in against the new server with her old password" "$CODE" 200
req b POST /api/login '{"username":"bob","password":"migrated-secret-1"}'; expect "bob's hashed password carried over and still works" "$CODE" 200
req n GET "/api/admin/users?pageSize=1"; expect "the people are all there" "$(jq_ "d['total']>30")" True
req n GET /api/admin/connect; expect "Dapper now treats the server as Dapper-run" "$(jq_ "d['dapperRun']")" True
req n GET /api/admin/migration; expect "rollback is offered" "$(jq_ "d['canRollback']")" True
expect "the first-start file is gone" "$(jq_ "d['newServer'] if 'newServer' in d else 'none'")" none

# --- roll back ---
req n POST /api/admin/migration/rollback '{}'; expect "rolling back needs approval" "$CODE" 400
req n POST /api/admin/migration/rollback '{"confirmed":true}'; expect "rollback works" "$CODE" 200
req o POST /api/login '{"username":"alice","password":"alicepass"}'; expect "signing in works against the old server again" "$CODE" 200
req o GET /api/admin/settings; expect "Dapper is back on the old server" "$(jq_ "d['settings']['ldap']['url']")" "ldap://source-ldap:389"
req o GET /api/admin/migration; expect "nothing left to roll back" "$(jq_ "d.get('canRollback', False)")" False
req o GET /api/admin/migration; expect "after a rollback the used-up server is explained, not called 'not running'" "$(jq_ "d['newServer']['reason']")" already-used
req o POST /api/admin/migration/finish '{"confirmed":true}'; expect "finishing when there is nothing to finish is refused" "$CODE" 409

echo; echo "passed $PASS, failed $FAIL"; [ "$FAIL" = 0 ]
