#!/usr/bin/env bash
# End-to-end tests for Unix (Linux login) accounts. Needs a configured Dapper plus the
# demo directory (see scripts/e2e.sh). Restores the Unix setting to "off" when done.
set -u
BASE=${BASE:-http://localhost:8080}
PASS=0; FAIL=0
J=$(mktemp -d); trap 'rm -rf "$J"' EXIT
req() { local jar=$1 m=$2 p=$3 d=${4:-}
  local out; out=$(curl -s -w '\n%{http_code}' -b "$J/$jar" -c "$J/$jar" -X "$m" -H 'X-Requested-With: dapper' -H 'Content-Type: application/json' ${d:+-d "$d"} "$BASE$p")
  CODE=${out##*$'\n'}; BODY=${out%$'\n'*}; }
jq_() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" <<<"$BODY"; }
check()  { if [ "$CODE" = "$2" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got $CODE, want $2): ${BODY:0:300}"; fi; }
expect() { if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got '$2', want '$3')"; fi; }
attr() { python3 -c "import sys,json;d=json.load(sys.stdin);print((d['attrs'].get(sys.argv[1]) or [''])[0])" "$1" <<<"$BODY"; }

req adm POST /api/login '{"username":"alice","password":"alicepass"}'; check "admin sign-in" 200
req adm GET /api/admin/settings; S=$BODY
mod() { python3 -c "import json,sys;d=json.loads(sys.argv[1])['settings'];exec(sys.argv[2]);print(json.dumps(d))" "$S" "$1"; }
U1=lx$RANDOM; U2=lx$RANDOM; U3=lx$RANDOM
dn() { echo "uid=$1,ou=people,dc=example,dc=org"; }
gdn() { echo "cn=$1,ou=groups,dc=example,dc=org"; }
newuser() { req adm POST /api/admin/users "{\"username\":\"$1\",\"attrs\":{\"givenName\":[\"Lin\"],\"sn\":[\"$1\"]$2}}"; }
cleanup() { for u in $U1 $U2 $U3 rawposix; do req adm DELETE "/api/admin/user?dn=$(dn $u)" >/dev/null; req adm DELETE "/api/admin/entry?dn=$(dn $u)" >/dev/null; req adm DELETE "/api/admin/entry?dn=$(gdn $u)" >/dev/null; done; }
trap 'cleanup; rm -rf "$J"' EXIT

# --- settings validation ---
req adm PUT /api/admin/settings "$(mod "d['unix'].update(enabled=True,uidMin=20,uidMax=10)")"; check "backwards ID range rejected" 400
req adm PUT /api/admin/settings "$(mod "d['unix'].update(enabled=True,homePattern='home/%u')")"; check "relative home pattern rejected" 400
req adm POST /api/admin/settings/check "$(mod "d['unix'].update(enabled=True,shadow=True,sshKeys=True)")"
expect "schema check passes and names what it verified" "$(jq_ "[c['message'] for c in d['checks'] if c['key']=='unix'][0]")" "The server supports Unix accounts (posixAccount, posixGroup, shadowAccount, ldapPublicKey)."

# --- enable Unix accounts (no ssh yet) ---
req adm PUT /api/admin/settings "$(mod "d['unix'].update(enabled=True,uidMin=10000,uidMax=10999,shadow=True,sshKeys=False)")"; check "enable Unix accounts" 200
req adm GET /api/me; expect "feature flag reaches the UI" "$(jq_ "d['features']['unix']")" True

# --- create ---
newuser $U1 ''; check "create a Unix account" 200
req adm GET "/api/admin/entry?dn=$(dn $U1)"
ID1=$(attr uidNumber)
[ "$ID1" -ge 10000 ] 2>/dev/null && expect "uid inside the configured range" 1 1 || expect "uid inside the configured range ($ID1)" 0 1
expect "gid equals uid" "$(attr gidNumber)" "$ID1"
expect "home directory from pattern" "$(attr homeDirectory)" "/home/$U1"
expect "default shell" "$(attr loginShell)" "/bin/bash"
expect "posixAccount class" "$(jq_ "'posixAccount' in d['attrs']['objectClass']")" True
expect "shadowAccount class" "$(jq_ "'shadowAccount' in d['attrs']['objectClass']")" True
req adm GET "/api/admin/entry?dn=$(gdn $U1)"; check "private group exists" 200
expect "private group is a posixGroup" "$(jq_ "d['attrs']['objectClass'][0]")" posixGroup
expect "private group gid matches" "$(attr gidNumber)" "$ID1"

newuser $U2 ',"homeDirectory":["/srv/'$U2'"],"loginShell":["/bin/zsh"]'; check "create with custom home and shell" 200
req adm GET "/api/admin/entry?dn=$(dn $U2)"; ID2=$(attr uidNumber)
expect "next free number is allocated" "$ID2" "$((ID1+1))"
expect "custom home kept" "$(attr homeDirectory)" "/srv/$U2"
expect "custom shell kept" "$(attr loginShell)" "/bin/zsh"

# the highest number in range wins even if it was created outside Dapper
req adm POST /api/admin/entry "{\"dn\":\"$(dn rawposix)\",\"attrs\":{\"objectClass\":[\"inetOrgPerson\",\"posixAccount\"],\"uid\":[\"rawposix\"],\"cn\":[\"Raw Posix\"],\"sn\":[\"Posix\"],\"uidNumber\":[\"10500\"],\"gidNumber\":[\"10500\"],\"homeDirectory\":[\"/home/rawposix\"]}}"
check "add an account outside Dapper" 200
newuser $U3 ''; req adm GET "/api/admin/entry?dn=$(dn $U3)"
expect "allocation skips past existing high ids" "$(attr uidNumber)" 10501

# --- overrides ---
req adm DELETE "/api/admin/user?dn=$(dn $U3)" >/dev/null
newuser $U3 ',"uidNumber":["10500"]'; check "taken UID refused" 400
newuser $U3 ',"uidNumber":["12"]'; check "system-range UID refused" 400
newuser $U3 ',"uidNumber":["abc"]'; check "non-numeric UID refused" 400
newuser $U3 ',"uidNumber":["10777"]'; check "free UID accepted" 200
req adm GET "/api/admin/entry?dn=$(dn $U3)"; expect "override used" "$(attr uidNumber)" 10777

# --- username collisions with groups ---
CLASH=clash$RANDOM
req adm POST /api/admin/groups "{\"name\":\"$CLASH\"}" >/dev/null
newuser $CLASH ''; check "username matching an existing group refused" 400
req adm DELETE "/api/admin/group?dn=$(gdn $CLASH)" >/dev/null

# --- editing ---
req adm GET "/api/admin/user?dn=$(dn $U1)"
expect "UID shown, read-only" "$(jq_ "[f['readOnly'] for f in d['fields'] if f['attr']=='uidNumber'][0]")" True
req adm PATCH "/api/admin/user?dn=$(dn $U1)" '{"attrs":{"uidNumber":["99999"]}}'; check "UID can't be edited" 403
req adm PATCH "/api/admin/user?dn=$(dn $U1)" '{"attrs":{"loginShell":["/bin/sh"],"homeDirectory":["/data/home/'$U1'"]}}'; check "admin edits shell and home" 200
req adm PATCH "/api/admin/user?dn=$(dn $U1)" '{"attrs":{"loginShell":["bash"]}}'; check "relative shell refused" 400
req adm PATCH "/api/admin/user?dn=$(dn bob)" '{"attrs":{"loginShell":["/bin/sh"]}}'; check "non-Unix account can't take Unix details" 400
req adm GET "/api/admin/user?dn=$(dn bob)"; expect "non-Unix account doesn't show Unix fields" "$(jq_ "len([f for f in d['fields'] if f['attr'] in ('uidNumber','loginShell')])")" 0

# --- suspend keeps the Unix account intact ---
req adm POST "/api/admin/user/suspend?dn=$(dn $U2)"; check "suspend a Unix account" 200
SDN=$(jq_ "d['dn']")
req adm GET "/api/admin/entry?dn=$SDN"; expect "uid unchanged after suspend" "$(attr uidNumber)" "$ID2"
req adm POST "/api/admin/user/restore?dn=$SDN"; check "restore" 200

# --- SSH keys ---
# (the demo OpenLDAP image already includes the openssh-lpk schema)
req adm PUT /api/admin/settings "$(mod "d['unix'].update(enabled=True,sshKeys=True)")"; check "enable SSH key management" 200
KEY='ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHQcFakeFakeFakeFakeFakeFakeFakeFakeFakeFakeFake test@laptop'
req adm POST /api/admin/user/password "?dn=$(dn $U1)" >/dev/null
req adm POST "/api/admin/user/password?dn=$(dn $U1)" '{"password":"lx-test-password"}'; check "set a known password" 200
req me POST /api/login "{\"username\":\"$U1\",\"password\":\"lx-test-password\"}"; check "Unix user signs in" 200
req me GET /api/me/profile
expect "user sees their UID (read-only)" "$(jq_ "[f['readOnly'] for f in d['fields'] if f['attr']=='uidNumber'][0]")" True
req me PATCH /api/me/profile '{"attrs":{"uidNumber":["0"]}}'; check "user can't change their UID" 403
req me PATCH /api/me/profile '{"attrs":{"loginShell":["/bin/sh"]}}'; check "user can't change their shell by default" 403
req me PATCH /api/me/profile '{"attrs":{"sshPublicKey":["not a key"]}}'; check "garbage key rejected" 400
req me PATCH /api/me/profile "{\"attrs\":{\"sshPublicKey\":[\"$KEY\"]}}"; check "user adds an SSH key" 200
req adm GET "/api/admin/entry?dn=$(dn $U1)"
expect "key stored" "$(jq_ "d['attrs']['sshPublicKey'][0]")" "$KEY"
expect "ldapPublicKey class added" "$(jq_ "'ldapPublicKey' in d['attrs']['objectClass']")" True
req me PATCH /api/me/profile '{"attrs":{"sshPublicKey":[]}}'; check "user removes the key" 200
# an account that predates Unix/SSH support can still add a key (class added on demand)
req bobj POST /api/login '{"username":"bob","password":"bobpass"}'; check "regular non-Unix user signs in" 200
req bobj PATCH /api/me/profile "{\"attrs\":{\"sshPublicKey\":[\"$KEY\"]}}"; check "existing account can add an SSH key" 200
req bobj PATCH /api/me/profile '{"attrs":{"sshPublicKey":[]}}' >/dev/null

# --- delete removes the private group ---
req adm DELETE "/api/admin/user?dn=$(dn $U1)"; check "delete a Unix account" 200
req adm GET "/api/admin/entry?dn=$(gdn $U1)"; check "private group deleted with the account" 404

# --- tidy up ---
req adm GET /api/admin/settings; S=$BODY
req adm PUT /api/admin/settings "$(mod "d['unix'].update(enabled=False,sshKeys=False,shadow=False)")"; check "turn Unix accounts back off" 200
req adm GET "/api/admin/user?dn=$(dn bob)"; expect "Unix fields hidden again when off" "$(jq_ "len([f for f in d['fields'] if f.get('feature')])")" 0

echo; echo "passed $PASS, failed $FAIL"
[ "$FAIL" = 0 ]
