#!/usr/bin/env bash
# End-to-end tests for changing Linux group IDs and finding group-number mismatches.
# Needs a configured Dapper plus the demo directory (see scripts/e2e.sh).
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

R=$((RANDOM%800)); B=$((20000+R*10))     # numbers unlikely to clash with anything else
pdn() { echo "uid=$1,ou=people,dc=example,dc=org"; }
gdn() { echo "cn=$1,ou=groups,dc=example,dc=org"; }
N=gx$R; P1=${N}a; P2=${N}b; P3=${N}c; P4=${N}d; GS=${N}shared; D1=${N}dup1; D2=${N}dup2
cleanup() { for u in $P1 $P2 $P3 $P4; do req adm DELETE "/api/admin/entry?dn=$(pdn $u)" >/dev/null; done
  for g in $P1 $P2 $P3 $P4 $GS $D1 $D2; do req adm DELETE "/api/admin/entry?dn=$(gdn $g)" >/dev/null; done; rm -rf "$J"; }
trap cleanup EXIT
mkperson() { req adm POST /api/admin/entry "{\"dn\":\"$(pdn $1)\",\"attrs\":{\"objectClass\":[\"inetOrgPerson\",\"posixAccount\"],\"uid\":[\"$1\"],\"cn\":[\"$1\"],\"sn\":[\"G\"],\"givenName\":[\"$1\"],\"uidNumber\":[\"$2\"],\"gidNumber\":[\"$3\"],\"homeDirectory\":[\"/home/$1\"]}}" >/dev/null; }
mkgroup()  { req adm POST /api/admin/entry "{\"dn\":\"$(gdn $1)\",\"attrs\":{\"objectClass\":[\"posixGroup\"],\"cn\":[\"$1\"],\"gidNumber\":[\"$2\"]}}" >/dev/null; }
gid_of() { req adm GET "/api/admin/entry?dn=$1"; jq_ "d['attrs']['gidNumber'][0]"; }
issues() { req adm GET /api/admin/groups/check; }

req adm POST /api/login '{"username":"alice","password":"alicepass"}'; check "admin sign-in" 200
req bob POST /api/login '{"username":"bob","password":"bobpass"}'
req bob GET /api/admin/groups/check; check "the check is admin-only" 403
req bob POST /api/admin/group/gid '{"dn":"x","gid":"1"}'; check "changing IDs is admin-only" 403

# --- mismatches like a real directory has ---
mkperson $P1 $((B+1)) $((B+1)); mkgroup $P1 $((B+2))           # personal group number differs from the person's
mkperson $P2 $((B+11)) $((B+11))                               # primary group number has no group at all
mkperson $P3 $((B+21)) $((B+21)); mkgroup $P3 $((B+21))        # fine, and used later to move along
mkperson $P4 $((B+31)) $((B+31)); mkgroup $P4 $((B+32))        # mismatch, fixed from the person's side
mkgroup $GS $((B+41)); mkgroup $D1 $((B+50)); mkgroup $D2 $((B+50))   # two groups sharing a number

issues
expect "a personal group with the wrong number is found" "$(jq_ "len([i for i in d['issues'] if i['kind']=='personal-mismatch' and '$P1' in i['title']])")" 1
expect "with a fix for each side" "$(jq_ "sorted(f['kind'] for i in d['issues'] if '$P1' in i['title'] and i['kind']=='personal-mismatch' for f in i['fixes'])")" "['groupGID', 'personGID']"
expect "a primary group number with no group is found" "$(jq_ "len([i for i in d['issues'] if i['kind']=='no-group' and '$P2' in i['title']])")" 1
expect "with a fix that creates the group" "$(jq_ "[f['kind'] for i in d['issues'] if i['kind']=='no-group' and '$P2' in i['title'] for f in i['fixes']]")" "['createGroup']"
expect "groups sharing a number are found" "$(jq_ "len([i for i in d['issues'] if i['kind']=='duplicate' and '$((B+50))' in i['title']])")" 1
expect "a consistent person and group aren't flagged" "$(jq_ "len([i for i in d['issues'] if '$P3' in i['title']])")" 0

# --- previewing a change ---
req adm POST /api/admin/group/gid "{\"dn\":\"$(gdn $GS)\",\"gid\":\"$((B+42))\"}"; check "preview a change" 200
expect "the preview changes nothing" "$(gid_of $(gdn $GS))" "$((B+41))"
expect "and warns that files on servers don't follow" "$(jq_ "any('Files on your servers' in w for w in d['warnings']) and 'chgrp' in d['chgrpHint']")" True
req adm POST /api/admin/group/gid "{\"dn\":\"$(gdn $GS)\",\"gid\":\"$((B+50))\"}"
expect "a number another group uses is reported as a conflict" "$(jq_ "'already has the ID' in d['conflict']")" True
req adm POST /api/admin/group/gid "{\"dn\":\"$(gdn $GS)\",\"gid\":\"$((B+50))\",\"confirmed\":true}"; check "and can't be applied" 400
expect "the group is untouched" "$(gid_of $(gdn $GS))" "$((B+41))"
for bad in 0 abc -5 99999999999; do req adm POST /api/admin/group/gid "{\"dn\":\"$(gdn $GS)\",\"gid\":\"$bad\"}"; check "invalid ID '$bad' refused" 400; done
req adm POST /api/admin/group/gid "{\"dn\":\"$(gdn $GS)\",\"gid\":\"$((B+41))\"}"; check "the same ID is refused" 400
req adm POST /api/admin/group/gid '{"dn":"cn=staff,ou=groups,dc=example,dc=org","gid":"5555"}'; check "a group with no ID (not a Linux group) is refused" 400
req adm POST /api/admin/group/gid '{"dn":"cn=admin,dc=example,dc=org","gid":"5555"}'; check "only managed groups can be changed" 400

# --- applying ---
req adm POST /api/admin/group/gid "{\"dn\":\"$(gdn $GS)\",\"gid\":\"$((B+42))\",\"confirmed\":true}"; check "change a group's ID" 200
expect "it took effect" "$(gid_of $(gdn $GS))" "$((B+42))"

# fix 1: give the personal group the number from the person's account
req adm POST /api/admin/group/gid "{\"dn\":\"$(gdn $P1)\",\"gid\":\"$((B+1))\"}"
expect "the preview shows the person who'd belong to it" "$(jq_ "[p['username'] for p in d['adopted']]")" "['$P1']"
req adm POST /api/admin/group/gid "{\"dn\":\"$(gdn $P1)\",\"gid\":\"$((B+1))\",\"confirmed\":true}"; check "fix the mismatch from the group's side" 200
issues; expect "that mismatch is gone" "$(jq_ "len([i for i in d['issues'] if '$P1' in i['title']])")" 0

# fix 2: create the missing group for a number people already use
req adm POST /api/admin/groups "{\"name\":\"$P2\",\"kind\":\"linux\",\"gid\":\"$((B+11))\"}"; check "creating a group at a number people already use is refused without approval to adopt it" 400
req adm POST /api/admin/groups "{\"name\":\"$P2\",\"kind\":\"linux\",\"gid\":\"$((B+11))\",\"adopt\":true}"; check "create the missing group, adopting the number" 200
issues; expect "the missing group is no longer reported" "$(jq_ "len([i for i in d['issues'] if '$P2' in i['title']])")" 0
req adm POST /api/admin/groups "{\"name\":\"${N}clash\",\"kind\":\"linux\",\"gid\":\"$((B+50))\",\"adopt\":true}"; check "adopting still can't reuse another group's number" 400

# fix 3: change the person's primary group number instead
req adm POST /api/admin/user/gid "{\"dn\":\"$(pdn $P4)\",\"gid\":\"$((B+99))\",\"confirmed\":true}"; check "a primary group number no group has is refused" 400
req adm POST /api/admin/user/gid "{\"dn\":\"$(pdn $P4)\",\"gid\":\"$((B+32))\"}"; expect "preview names the group they'd join" "$(jq_ "d['group']")" "$P4"
req adm POST /api/admin/user/gid "{\"dn\":\"$(pdn $P4)\",\"gid\":\"$((B+32))\",\"confirmed\":true}"; check "fix the mismatch from the person's side" 200
expect "the person's number changed" "$(gid_of $(pdn $P4))" "$((B+32))"
req adm POST /api/admin/user/gid "{\"dn\":\"$(pdn bob)\",\"gid\":\"1\"}"; check "a non-Linux account has no primary group to change" 400

# --- moving people along with their group ---
req adm POST /api/admin/group/gid "{\"dn\":\"$(gdn $P3)\",\"gid\":\"$((B+22))\",\"movePeople\":true}"
expect "the preview lists who would move along" "$(jq_ "[p['username'] for p in d['moving']]")" "['$P3']"
req adm POST /api/admin/group/gid "{\"dn\":\"$(gdn $P3)\",\"gid\":\"$((B+22))\",\"movePeople\":true,\"confirmed\":true}"; check "change the ID and move people with it" 200
expect "the group has the new number" "$(gid_of $(gdn $P3))" "$((B+22))"
expect "and so does the person" "$(gid_of $(pdn $P3))" "$((B+22))"
issues; expect "nothing is left mismatched for them" "$(jq_ "len([i for i in d['issues'] if '$P3' in i['title']])")" 0

echo; echo "passed $PASS, failed $FAIL"
[ "$FAIL" = 0 ]
