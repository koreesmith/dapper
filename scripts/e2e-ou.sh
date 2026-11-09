#!/usr/bin/env bash
# End-to-end tests for organizational units: list, create, rename, delete, and moving people. Needs a configured Dapper plus the demo directory.
set -u
BASE=${BASE:-http://localhost:8080}
PASS=0; FAIL=0
J=$(mktemp -d)
req() { local jar=$1 m=$2 p=$3 d=${4:-}
  local out; out=$(curl -s -w '\n%{http_code}' -b "$J/$jar" -c "$J/$jar" -X "$m" -H 'X-Requested-With: dapper' -H 'Content-Type: application/json' ${d:+-d "$d"} "$BASE$p")
  CODE=${out##*$'\n'}; BODY=${out%$'\n'*}; }
jq_() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" <<<"$BODY"; }
check()  { if [ "$CODE" = "$2" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got $CODE, want $2): ${BODY:0:300}"; fi; }
expect() { if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got '$2', want '$3')"; fi; }
R=$RANDOM; T=ou$R
PEOPLE="ou=people,dc=example,dc=org"
SDN="cn=staff,ou=groups,dc=example,dc=org"
cleanup() {
  for u in $T.a $T.b; do for b in "$PEOPLE" "ou=$T-sales,$PEOPLE" "ou=$T-eng,$PEOPLE"; do req adm DELETE "/api/admin/user?dn=uid=$u,$b" >/dev/null; done; done
  for o in "$T-sales" "$T-eng" "$T-new" "$T-inner"; do req adm DELETE "/api/admin/ou?dn=ou=$o,ou=$T-sales,$PEOPLE" >/dev/null; req adm DELETE "/api/admin/ou?dn=ou=$o,$PEOPLE" >/dev/null; done
  rm -rf "$J"; }
trap cleanup EXIT

req adm POST /api/login '{"username":"alice","password":"alicepass"}'; check "admin sign-in" 200
req bob POST /api/login '{"username":"bob","password":"bobpass"}'
req bob GET /api/admin/ous; check "listing units is admin-only" 403
req bob POST /api/admin/ous "{\"parent\":\"$PEOPLE\",\"name\":\"x\"}"; check "creating units is admin-only" 403

req adm GET /api/admin/ous; check "list units" 200
expect "the people container comes first" "$(jq_ "d['units'][0]['root']")" True

# create
req adm POST /api/admin/ous "{\"parent\":\"$PEOPLE\",\"name\":\"$T-sales\"}"; check "create a unit" 200
req adm POST /api/admin/ous "{\"parent\":\"$PEOPLE\",\"name\":\"$T-sales\"}"; check "creating the same unit again is refused" 409
req adm POST /api/admin/ous "{\"parent\":\"$PEOPLE\",\"name\":\"bad,name\"}"; check "odd characters are refused" 400
req adm POST /api/admin/ous "{\"parent\":\"ou=groups,dc=example,dc=org\",\"name\":\"$T-x\"}"; check "units can't be made outside the people container" 400
req adm POST /api/admin/ous "{\"parent\":\"ou=$T-sales,$PEOPLE\",\"name\":\"$T-inner\"}"; check "a unit can sit inside a unit" 200
req adm GET /api/admin/ous
expect "nesting is shown" "$(jq_ "[u['depth'] for u in d['units'] if u['name']=='$T-inner'][0]")" 2
req adm DELETE "/api/admin/ou?dn=ou=$T-inner,ou=$T-sales,$PEOPLE"; check "remove the inner unit" 200
req adm DELETE "/api/admin/ou?dn=$PEOPLE"; check "the people container can't be removed" 400

# create a person inside a unit
req adm POST /api/admin/users "{\"username\":\"$T.a\",\"attrs\":{\"givenName\":[\"Ann\"],\"sn\":[\"$T\"]},\"password\":\"Passw0rd!x1\",\"ou\":\"ou=$T-sales,$PEOPLE\"}"; check "create a person in a unit" 200
req adm GET "/api/admin/user?dn=uid=$T.a,ou=$T-sales,$PEOPLE"; check "they live in that unit" 200
expect "and detail says where" "$(jq_ "d['ou']")" "ou=$T-sales,$PEOPLE"
req adm POST /api/admin/users "{\"username\":\"$T.b\",\"attrs\":{\"givenName\":[\"Ben\"],\"sn\":[\"$T\"]},\"password\":\"Passw0rd!x1\",\"ou\":\"ou=groups,dc=example,dc=org\"}"; check "can't create a person outside the people tree" 400

# a unit with people in it
req adm DELETE "/api/admin/ou?dn=ou=$T-sales,$PEOPLE"; check "a unit with people can't be removed" 400
req adm POST /api/admin/ous/rename "{\"dn\":\"ou=$T-sales,$PEOPLE\",\"name\":\"$T-new\"}"; check "a unit with people can't be renamed" 400

# move, with group membership following
req adm POST "/api/admin/group/members?dn=$SDN" "{\"member\":\"uid=$T.a,ou=$T-sales,$PEOPLE\"}"; check "add them to a group" 200
req adm POST /api/admin/ous "{\"parent\":\"$PEOPLE\",\"name\":\"$T-eng\"}"; check "create a second unit" 200
req adm POST /api/admin/user/move "{\"dn\":\"uid=$T.a,ou=$T-sales,$PEOPLE\",\"ou\":\"ou=$T-eng,$PEOPLE\"}"; check "move a person" 200
req adm GET "/api/admin/user?dn=uid=$T.a,ou=$T-eng,$PEOPLE"; check "they're in the new unit" 200
req adm POST /api/admin/user/move "{\"dn\":\"uid=$T.a,ou=$T-eng,$PEOPLE\",\"ou\":\"ou=$T-eng,$PEOPLE\"}"; check "moving to where they already are is refused" 400
req adm POST /api/admin/user/move "{\"dn\":\"uid=$T.a,ou=$T-eng,$PEOPLE\",\"ou\":\"ou=groups,dc=example,dc=org\"}"; check "can't move outside the people tree" 400
req adm GET "/api/admin/group?dn=$SDN"
BODYG=$BODY
expect "group membership followed them" "$(echo "$BODYG" | grep -c "uid=$T.a,ou=$T-eng")" 1

# rename and delete an empty unit
req adm POST /api/admin/user/move "{\"dn\":\"uid=$T.a,ou=$T-eng,$PEOPLE\",\"ou\":\"$PEOPLE\"}"; check "move them back to the top" 200
req adm POST /api/admin/ous/rename "{\"dn\":\"ou=$T-eng,$PEOPLE\",\"name\":\"$T-new\"}"; check "rename an empty unit" 200
req adm DELETE "/api/admin/ou?dn=ou=$T-new,$PEOPLE"; check "remove an empty unit" 200
req adm DELETE "/api/admin/ou?dn=ou=$T-sales,$PEOPLE"; check "remove the other empty unit" 200
req adm GET "/api/admin/group?dn=$SDN"
expect "group still lists them at the top" "$(echo "$BODY" | grep -c "uid=$T.a,$PEOPLE")" 1

echo; echo "$PASS passed, $FAIL failed"; [ "$FAIL" = 0 ]
