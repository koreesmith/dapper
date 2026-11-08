#!/usr/bin/env bash
# End-to-end API checks against a running Dapper + the dev OpenLDAP seed data
# (deploy/dev-seed.ldif). Usage: scripts/e2e.sh [base-url]
set -u
BASE=${1:-http://localhost:8080}
PASS=0; FAIL=0
J=$(mktemp -d); trap 'rm -rf "$J"' EXIT

# req <jar> <method> <path> [json]  -> sets CODE and BODY
req() {
  local jar=$1 m=$2 p=$3 d=${4:-}
  local out; out=$(curl -s -w '\n%{http_code}' -b "$J/$jar" -c "$J/$jar" -X "$m" \
    -H 'X-Requested-With: dapper' -H 'Content-Type: application/json' ${d:+-d "$d"} "$BASE$p")
  CODE=${out##*$'\n'}; BODY=${out%$'\n'*}
}
jq_() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" <<<"$BODY"; }
check() { # check <name> <expected-code>
  if [ "$CODE" = "$2" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got $CODE, want $2): $BODY"; fi
}
expect() { # expect <name> <actual> <wanted>
  if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got '$2', want '$3')"; fi
}
login() { req "$1" POST /api/login "{\"username\":\"$2\",\"password\":\"$3\"}"; }

U=e2e$RANDOM
UDN="uid=$U,ou=people,dc=example,dc=org"

login admin alice alicepass;               check "admin login" 200
login bob bob bobpass;                     check "user login" 200
req bob GET /api/admin/users;              check "user blocked from admin API" 403
req admin GET "/api/admin/users?q=bob";    check "list+search" 200
expect "search finds bob" "$(jq_ "d['total']")" 1

# --- create ---
req admin POST /api/admin/users "{\"username\":\"$U\",\"attrs\":{\"givenName\":[\"Dana\"],\"sn\":[\"Test\"],\"mail\":[\"$U@example.org\"],\"telephoneNumber\":[\"+1 555 0999\"]},\"groups\":[\"cn=staff,ou=groups,dc=example,dc=org\"]}"
check "create user" 200
TEMP=$(jq_ "d.get('temporaryPassword','')")
[ -n "$TEMP" ] && expect "temp password returned" 1 1 || expect "temp password returned" 0 1
req admin POST /api/admin/users "{\"username\":\"$U\",\"attrs\":{\"givenName\":[\"X\"],\"sn\":[\"Y\"]}}"; check "duplicate username rejected" 400
req admin POST /api/admin/users '{"username":"Bad Name!","attrs":{"givenName":["X"],"sn":["Y"]}}'; check "bad username rejected" 400
req admin POST /api/admin/users '{"username":"nolast","attrs":{"givenName":["X"]}}'; check "missing last name rejected" 400
req admin POST /api/admin/users "{\"username\":\"badmail1\",\"attrs\":{\"givenName\":[\"X\"],\"sn\":[\"Y\"],\"mail\":[\"nope\"]}}"; check "bad email rejected" 400
login dana "$U" "$TEMP";                   check "new user signs in with temp password" 200
expect "new user is not admin" "$(jq_ "d['isAdmin']")" False

# --- admin edit / detail ---
req admin PATCH "/api/admin/user?dn=$UDN" '{"attrs":{"title":["Tester"],"mobile":["+1 555 0998"]}}'; check "admin edit" 200
req admin GET "/api/admin/user?dn=$UDN";   check "detail" 200
expect "detail shows title" "$(jq_ "[f['values'] for f in d['fields'] if f['attr']=='title'][0][0]")" Tester
expect "detail shows group" "$(jq_ "d['groups'][0]['name']")" staff
req admin GET "/api/admin/user?dn=cn=admin,dc=example,dc=org"; check "can't open non-user entries" 400

# --- self-service ---
req dana GET /api/me/profile;              check "own profile" 200
req dana PATCH /api/me/profile '{"attrs":{"l":["Springfield"]}}'; check "user edits allowed field" 200
req dana PATCH /api/me/profile '{"attrs":{"givenName":["Hacked"]}}'; check "user can't edit locked field" 403
req dana PATCH /api/me/profile '{"attrs":{"description":["x"]}}'; check "user can't edit admin-only field" 403
req dana PATCH /api/me/profile '{"attrs":{"userPassword":["x"]}}'; check "user can't write arbitrary attributes" 400
req dana POST /api/me/password "{\"current\":\"wrong\",\"new\":\"Newpassw0rd!\"}"; check "wrong current password" 400
req dana POST /api/me/password "{\"current\":\"$TEMP\",\"new\":\"short\"}"; check "weak new password" 400
req dana POST /api/me/password "{\"current\":\"$TEMP\",\"new\":\"Newpassw0rd!\"}"; check "change password" 200
login dana2 "$U" "Newpassw0rd!";           check "sign in with new password" 200
login dana3 "$U" "$TEMP";                  check "old password rejected" 401

# --- field policy ---
req admin PUT /api/admin/settings/fields '{"fields":[{"attr":"givenName","userEditable":true}]}'; check "policy update" 200
req dana PATCH /api/me/profile '{"attrs":{"givenName":["Danielle"]}}'; check "policy change takes effect" 200
req admin PUT /api/admin/settings/fields '{"fields":[{"attr":"mail","userEditable":true},{"attr":"telephoneNumber","userEditable":true},{"attr":"mobile","userEditable":true},{"attr":"street","userEditable":true},{"attr":"l","userEditable":true},{"attr":"st","userEditable":true},{"attr":"postalCode","userEditable":true}]}'; check "policy restored" 200
req admin PUT /api/admin/settings/fields '{"fields":[{"attr":"userPassword","userEditable":true}]}'; check "policy rejects unknown attrs" 400
req admin PATCH "/api/admin/user?dn=$UDN" '{"attrs":{"givenName":[""]}}'; check "required field can't be blanked" 400

# --- suspend / restore ---
req admin POST "/api/admin/user/suspend?dn=$UDN"; check "suspend" 200
SDN=$(jq_ "d['dn']")
login dana4 "$U" "Newpassw0rd!";           check "suspended user can't sign in" 401
req admin GET "/api/admin/users?q=$U&status=suspended"; expect "listed as suspended" "$(jq_ "d['total']")" 1
req admin GET "/api/admin/user?dn=$SDN";   expect "group membership followed the move" "$(jq_ "len(d['groups'])")" 1
req admin POST "/api/admin/user/restore?dn=$SDN"; check "restore" 200
login dana5 "$U" "Newpassw0rd!";           check "restored user can sign in" 200
req admin POST "/api/admin/user/suspend?dn=uid=alice,ou=people,dc=example,dc=org"; check "can't suspend yourself" 400
req admin DELETE "/api/admin/user?dn=uid=alice,ou=people,dc=example,dc=org"; check "can't delete yourself" 400

# --- admin password reset ---
req admin POST "/api/admin/user/password?dn=$UDN" '{}'; check "admin reset password" 200
T2=$(jq_ "d['temporaryPassword']")
login dana6 "$U" "$T2";                    check "sign in with reset password" 200

# --- groups ---
G="e2e-group-$RANDOM"; GDN="cn=$G,ou=groups,dc=example,dc=org"
req admin POST /api/admin/groups "{\"name\":\"$G\"}"; check "create group" 200
req admin POST "/api/admin/group/members?dn=$GDN" "{\"member\":\"$UDN\"}"; check "add member" 200
req admin GET "/api/admin/group?dn=$GDN"; expect "member listed" "$(jq_ "d['members'][0]['username']")" "$U"
req admin DELETE "/api/admin/group/members?dn=$GDN&member=$UDN"; check "remove last member" 200
req admin GET "/api/admin/group?dn=$GDN"; expect "empty group stays valid" "$(jq_ "len(d['members'])")" 0
req admin DELETE "/api/admin/group?dn=$GDN"; check "delete group" 200
req admin DELETE "/api/admin/group?dn=cn=dapper-admins,ou=groups,dc=example,dc=org"; check "admin group protected" 400
req admin DELETE "/api/admin/group/members?dn=cn=dapper-admins,ou=groups,dc=example,dc=org&member=uid=alice,ou=people,dc=example,dc=org"; check "can't drop yourself from admins" 400

# --- delete ---
req admin DELETE "/api/admin/user?dn=$UDN"; check "delete user" 200
req admin GET "/api/admin/users?q=$U"; expect "user gone" "$(jq_ "d['total']")" 0
req admin GET "/api/admin/group?dn=cn=staff,ou=groups,dc=example,dc=org"; expect "removed from groups on delete" "$(jq_ "len([m for m in d['members'] if m['username']=='$U'])")" 0

# --- hardening ---
CODE=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE/api/logout"); expect "CSRF header required" "$CODE" 403
for i in 1 2 3 4 5; do login brute2 victim wrong; done
login brute2 victim wrong;                 check "login rate limit" 429
req admin GET /healthz;                    check "healthz" 200

echo; echo "passed $PASS, failed $FAIL"
[ "$FAIL" = 0 ]
