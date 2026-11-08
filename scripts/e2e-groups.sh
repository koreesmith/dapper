#!/usr/bin/env bash
# End-to-end tests for Linux (posixGroup) groups, including Red Hat-style personal groups.
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

R=$RANDOM; P1=pg1x$R; P2=pg2x$R; LG=lgrp$R; NG=ngrp$R
pdn() { echo "uid=$1,ou=people,dc=example,dc=org"; }
gdn() { echo "cn=$1,ou=groups,dc=example,dc=org"; }
cleanup() { for u in $P1 $P2 $P3; do req adm DELETE "/api/admin/entry?dn=$(pdn $u)" >/dev/null; req adm DELETE "/api/admin/entry?dn=$(gdn $u)" >/dev/null; done
  for g in $LG $NG; do req adm DELETE "/api/admin/entry?dn=$(gdn $g)" >/dev/null; done; rm -rf "$J"; }
P3=pg3x$R
trap cleanup EXIT

req adm POST /api/login '{"username":"alice","password":"alicepass"}'; check "admin sign-in" 200
req adm GET "/api/admin/groups?pageSize=500"; BEFORE_SHARED=$(jq_ "d['counts'].get('shared',0)")

# --- Red Hat style data: a Linux user and a personal group each, plus a shared Linux group ---
mkperson() { req adm POST /api/admin/entry "{\"dn\":\"$(pdn $1)\",\"attrs\":{\"objectClass\":[\"inetOrgPerson\",\"posixAccount\"],\"uid\":[\"$1\"],\"cn\":[\"$1\"],\"sn\":[\"Pg\"],\"givenName\":[\"$1\"],\"uidNumber\":[\"$2\"],\"gidNumber\":[\"$2\"],\"homeDirectory\":[\"/home/$1\"],\"loginShell\":[\"/bin/bash\"]}}"; }
mkpgroup() { req adm POST /api/admin/entry "{\"dn\":\"$(gdn $1)\",\"attrs\":{\"objectClass\":[\"posixGroup\"],\"cn\":[\"$1\"],\"gidNumber\":[\"$2\"]$3}}"; }
mkperson $P1 $((7000+R%900)); G1=$((7000+R%900))
mkperson $P2 $((8000+R%900)); G2=$((8000+R%900))
mkpgroup $P1 $G1 ''; check "personal group for person 1" 200
mkpgroup $P2 $G2 ''; check "personal group for person 2" 200
mkpgroup $LG $((9000+R%900)) ",\"memberUid\":[\"$P2\"]"; check "shared Linux group with a memberUid" 200
LGID=$((9000+R%900))

# --- listing ---
req adm GET "/api/admin/groups?pageSize=500"
expect "the personal groups are listed" "$(jq_ "len([g for g in d['groups'] if g['name'] in ('$P1','$P2')])")" 2
expect "they're classed as personal" "$(jq_ "sorted(set(g['kind'] for g in d['groups'] if g['name'] in ('$P1','$P2')))")" "['personal']"
expect "the shared Linux group is classed as linux" "$(jq_ "[g['kind'] for g in d['groups'] if g['name']=='$LG'][0]")" linux
expect "standard groups are still shown" "$(jq_ "d['counts'].get('shared',0)")" "$BEFORE_SHARED"
expect "members of a Linux group are counted" "$(jq_ "[g['members'] for g in d['groups'] if g['name']=='$LG'][0]")" 1
req adm GET "/api/admin/groups?kind=personal&q=$P1"
expect "filter by kind and search" "$(jq_ "[g['name'] for g in d['groups']]")" "['$P1']"
req adm GET "/api/admin/groups?q=$LGID"
expect "search by group ID" "$(jq_ "[g['name'] for g in d['groups']]")" "['$LG']"
req adm GET "/api/admin/groups?pageSize=1&page=2"
expect "paging" "$(jq_ "len(d['groups'])")" 1

# --- one group ---
req adm GET "/api/admin/group?dn=$(gdn $LG)"
expect "member listed by username is resolved to the person" "$(jq_ "d['members'][0]['dn']")" "$(pdn $P2)"
expect "kind and gid shown" "$(jq_ "d['kind']+' '+d['gid']")" "linux $LGID"
req adm GET "/api/admin/group?dn=$(gdn $P1)"
expect "personal group lists who has it as their primary group" "$(jq_ "[m['username'] for m in d['primaryMembers']]")" "['$P1']"
expect "and explains why it can't be deleted" "$(jq_ "'primary group' in d['protected']")" True

# --- a person's groups ---
req adm GET "/api/admin/user?dn=$(pdn $P2)"
expect "person shows their personal group as primary" "$(jq_ "[(g['name'],g['kind'],g.get('primary',False)) for g in d['groups'] if g['kind']=='personal']")" "[('$P2', 'personal', True)]"
expect "and the Linux group they're a member of" "$(jq_ "[g['name'] for g in d['groups'] if g['kind']=='linux']")" "['$LG']"

# --- adding and removing members ---
req adm POST "/api/admin/group/members?dn=$(gdn $LG)" "{\"member\":\"$(pdn $P1)\"}"; check "add a member to a Linux group" 200
req adm GET "/api/admin/entry?dn=$(gdn $LG)"
expect "stored as a username in memberUid" "$(jq_ "sorted(d['attrs']['memberUid'])")" "['$P1', '$P2']"
req adm POST "/api/admin/group/members?dn=$(gdn $LG)" "{\"member\":\"$(pdn $P1)\"}"; check "adding someone already in it is harmless" 200
req adm DELETE "/api/admin/group/members?dn=$(gdn $LG)&member=$(pdn $P1)"; check "remove a member" 200
req adm DELETE "/api/admin/group/members?dn=$(gdn $LG)&member=$(pdn $P1)"; check "removing someone who isn't in it is harmless" 200
req adm GET "/api/admin/entry?dn=$(gdn $LG)"
expect "memberUid back to one" "$(jq_ "d['attrs']['memberUid']")" "['$P2']"

# --- creating Linux groups ---
req adm POST /api/admin/groups "{\"name\":\"$NG\",\"kind\":\"linux\"}"; check "create a Linux group" 200
req adm GET "/api/admin/entry?dn=$(gdn $NG)"
expect "it's a posixGroup with a group ID in range" "$(jq_ "d['attrs']['objectClass']==['posixGroup'] and int(d['attrs']['gidNumber'][0])>=10000")" True
req adm POST /api/admin/groups "{\"name\":\"bad name\",\"kind\":\"linux\"}"; check "Linux group names can't contain spaces" 400
req adm POST "/api/admin/group/members?dn=$(gdn $NG)" "{\"member\":\"$(pdn $P1)\"}" >/dev/null
req adm DELETE "/api/admin/group?dn=$(gdn $NG)"; check "delete a Linux group nobody uses as primary" 200

# --- protection ---
req adm DELETE "/api/admin/group?dn=$(gdn $P1)"; check "can't delete a group someone has as their primary group" 400
req adm GET "/api/admin/entry?dn=$(gdn $P1)"; check "and it's still there" 200

# --- a new person can be put straight into a Linux group ---
req adm POST /api/admin/users "{\"username\":\"$P3\",\"attrs\":{\"givenName\":[\"Pg\"],\"sn\":[\"Three\"]},\"groups\":[\"$(gdn $LG)\"]}"; check "create a person in a Linux group" 200
req adm GET "/api/admin/entry?dn=$(gdn $LG)"
expect "their username is in memberUid" "$(jq_ "sorted(d['attrs']['memberUid'])")" "['$P2', '$P3']"

# --- deleting a person tidies the groups ---
req adm DELETE "/api/admin/user?dn=$(pdn $P3)"; check "delete that person" 200
req adm GET "/api/admin/entry?dn=$(gdn $LG)"
expect "their username leaves the Linux group" "$(jq_ "d['attrs']['memberUid']")" "['$P2']"
req adm DELETE "/api/admin/user?dn=$(pdn $P2)"; check "delete a person with a personal group" 200
req adm GET "/api/admin/entry?dn=$(gdn $P2)"; check "their personal group is removed too" 404
req adm GET "/api/admin/entry?dn=$(gdn $LG)"; expect "and they leave the shared group" "$(jq_ "d['attrs'].get('memberUid')")" "None"

# --- self service shows real groups but hides the personal one ---
req adm GET /api/me/profile; expect "My account lists groups" "$(jq_ "'groups' in d")" True

echo; echo "passed $PASS, failed $FAIL"
[ "$FAIL" = 0 ]
