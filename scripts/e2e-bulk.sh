#!/usr/bin/env bash
# End-to-end tests for CSV export/import and bulk actions. Needs a configured Dapper plus the demo directory.
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
R=$RANDOM; T=bk$R
pdn() { echo "uid=$1,ou=people,dc=example,dc=org"; }
SDN="cn=staff,ou=groups,dc=example,dc=org"
cleanup() { for u in ${T}.ada ${T}.grace ${T}.alan ${T}.kay ${T}.x1 ${T}.x2 ${T}.x3 ${T}formula custom$R; do req adm DELETE "/api/admin/user?dn=$(pdn $u)" >/dev/null; req adm DELETE "/api/admin/user?dn=uid=$u,ou=suspended,dc=example,dc=org" >/dev/null; done; rm -rf "$J"; }
trap cleanup EXIT
imp() { python3 -c "import json,sys;print(json.dumps({'csv':sys.argv[1],'dryRun':sys.argv[2]=='1','groups':sys.argv[3:]}))" "$1" "$2" "${@:3}"; }

req adm POST /api/login '{"username":"alice","password":"alicepass"}'; check "admin sign-in" 200
req bob POST /api/login '{"username":"bob","password":"bobpass"}'
req bob POST /api/admin/users/import '{"csv":"x","dryRun":true}'; check "import is admin-only" 403
req bob POST /api/admin/users/bulk '{"action":"suspend","dns":["x"]}'; check "bulk actions are admin-only" 403

# --- export ---
curl -s -b "$J/adm" -H 'X-Requested-With: dapper' "$BASE/api/admin/users?format=csv" -o "$J/all.csv" -w '%{http_code} %{content_type}\n' | head -1 | grep -q '^200 text/csv' && CODE=200 || CODE=0
check "export everyone as CSV" 200
expect "with a header row" "$(head -1 $J/all.csv)" "username,first name,last name,display name,email,phone,title,department,status"
expect "and the people in it" "$(grep -c '^alice,' $J/all.csv)" 1
curl -s -b "$J/adm" -H 'X-Requested-With: dapper' "$BASE/api/admin/users?format=csv&q=bob" -o "$J/bob.csv"
expect "the export follows the search" "$(($(wc -l < $J/bob.csv) - 1))" 1

# --- import: check first, nothing is created ---
CSV=$'First Name,Last Name,Email,Phone,Username\n'"Ada,${T},ada@example.org,+1 555 0111,${T}.ada"$'\n'"Grace,${T},grace@example.org,,${T}.grace"$'\n'"Alan,${T},not-an-email,,${T}.alan"$'\n'"Kay,,kay@example.org,,${T}.kay"$'\n'"Dup,${T},d@example.org,,${T}.ada"$'\n'"Bob,Again,b@example.org,,bob"
req adm POST /api/admin/users/import "$(imp "$CSV" 1)"; check "check an import file" 200
expect "nothing was created by the check" "$(req adm GET "/api/admin/users?q=$T"; jq_ "d['total']")" 0
req adm POST /api/admin/users/import "$(imp "$CSV" 1)"
expect "two rows are fine" "$(jq_ "d['ready']")" 2
expect "bad email is explained on its row" "$(jq_ "[r['message'] for r in d['rows'] if r['username']=='${T}.alan'][0]")" "\"not-an-email\" is not a valid email address"
expect "a missing last name is explained" "$(jq_ "'Last name' in [r['message'] for r in d['rows'] if r['username']=='${T}.kay'][0]")" True
expect "a repeated username in the file is caught" "$(jq_ "'also on row' in [r['message'] for r in d['rows'] if r['row']==6][0]")" True
expect "a username already in the directory is caught" "$(jq_ "'already taken' in [r['message'] for r in d['rows'] if r['username']=='bob'][0]")" True

# --- import for real ---
GOOD=$'first name,last name,email,username\n'"Ada,${T},ada@example.org,${T}.ada"$'\n'"Grace,${T},grace@example.org,${T}.grace"$'\n'"Bad,${T},nope,${T}.alan"
req adm POST /api/admin/users/import "$(imp "$GOOD" 0 "$SDN")"; check "import" 200
expect "the good rows were created" "$(jq_ "d['created']")" 2
expect "the bad row was reported, not created" "$(jq_ "d['failed']")" 1
expect "each new person gets a temporary password" "$(jq_ "all(len(r.get('temporaryPassword',''))>=12 for r in d['rows'] if r['status']=='created')")" True
TEMP=$(jq_ "[r['temporaryPassword'] for r in d['rows'] if r['username']=='${T}.ada'][0]")
req ada POST /api/login "{\"username\":\"${T}.ada\",\"password\":\"$TEMP\"}"; check "an imported person can sign in with their temporary password" 200
req adm GET "/api/admin/group?dn=$SDN"
expect "and were put in the chosen group" "$(jq_ "len([m for m in d['members'] if m['username'].startswith('${T}.')])")" 2
req adm POST /api/admin/users/import "$(imp "$GOOD" 0)"
expect "running the same import again creates nothing" "$(jq_ "d['created']")" 0
req adm POST /api/admin/users/import "$(imp 'first name,last name
A,B' 0)" >/dev/null
BIG=$(python3 -c "print('first name,last name\n'+'A,B\n'*501)")
req adm POST /api/admin/users/import "$(imp "$BIG" 1)"; check "more than 500 rows is refused" 400
req adm POST /api/admin/users/import "$(imp 'nothing useful' 1)"; check "a file without the needed columns is refused" 400

# a name that a spreadsheet would run as a formula is neutralised on export
req adm POST /api/admin/users "{\"username\":\"${T}formula\",\"attrs\":{\"givenName\":[\"=1+1\"],\"sn\":[\"Sheet\"]}}" >/dev/null
curl -s -b "$J/adm" -H 'X-Requested-With: dapper' "$BASE/api/admin/users?format=csv&q=${T}formula" -o "$J/f.csv"
expect "formulas in names are neutralised in the export" "$(grep -c "^${T}formula,'=1+1," $J/f.csv)" 1

# --- bulk actions ---
A1=$(pdn ${T}.ada); A2=$(pdn ${T}.grace)
req adm POST /api/admin/users/bulk "{\"action\":\"suspend\",\"dns\":[\"$A1\",\"$A2\"]}"; check "suspend two people at once" 200
expect "both were suspended" "$(jq_ "d['done']")" 2
req adm GET "/api/admin/users?q=$T&status=suspended"; expect "the list shows them as suspended" "$(jq_ "d['total']")" 2
req adm POST /api/admin/users/bulk "{\"action\":\"restore\",\"dns\":[\"uid=${T}.ada,ou=suspended,dc=example,dc=org\",\"uid=${T}.grace,ou=suspended,dc=example,dc=org\"]}"
expect "and restored together" "$(jq_ "d['done']")" 2
req adm POST /api/admin/users/bulk "{\"action\":\"removeGroup\",\"dns\":[\"$A1\",\"$A2\"],\"group\":\"$SDN\"}"; expect "remove several from a group" "$(jq_ "d['done']")" 2
req adm POST /api/admin/users/bulk "{\"action\":\"addGroup\",\"dns\":[\"$A1\",\"$A2\"],\"group\":\"$SDN\"}"; expect "add several to a group" "$(jq_ "d['done']")" 2
req adm POST /api/admin/users/bulk "{\"action\":\"suspend\",\"dns\":[\"$(pdn alice)\",\"$A1\"]}"
expect "you can't suspend yourself, but the others still go through" "$(jq_ "d['done']==1 and d['failed']==1 and 'your own' in [r['message'] for r in d['results'] if not r['ok']][0]")" True
req adm POST /api/admin/users/bulk "{\"action\":\"restore\",\"dns\":[\"uid=${T}.ada,ou=suspended,dc=example,dc=org\"]}" >/dev/null
req adm POST /api/admin/users/bulk "{\"action\":\"delete\",\"dns\":[\"$A1\"]}"; check "deleting needs confirmation" 400
req adm POST /api/admin/users/bulk "{\"action\":\"delete\",\"dns\":[\"$(pdn alice)\",\"cn=admin,dc=example,dc=org\",\"$A1\"],\"confirmed\":true}"
expect "deleting skips yourself and anything that isn't a managed account" "$(jq_ "d['done']==1 and d['failed']==2")" True
req adm GET "/api/admin/users?q=${T}.ada"; expect "the deleted person is gone" "$(jq_ "d['total']")" 0
req adm POST /api/admin/users/bulk '{"action":"explode","dns":["x"]}'; check "unknown actions are refused" 400
req adm POST /api/admin/users/bulk '{"action":"suspend","dns":[]}'; check "an empty selection is refused" 400
req adm POST /api/admin/users/bulk "{\"action\":\"removeGroup\",\"dns\":[\"$(pdn alice)\"],\"group\":\"cn=dapper-admins,ou=groups,dc=example,dc=org\"}"; check "you can't remove yourself from the administrators group in bulk either" 400
req adm GET "/api/admin/audit?q=user.bulk"; expect "bulk changes are recorded" "$(jq_ "d['total']>=1")" True

echo; echo "passed $PASS, failed $FAIL"
[ "$FAIL" = 0 ]
