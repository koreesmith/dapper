#!/usr/bin/env bash
# End-to-end security regression tests: the service-account password never follows a changed server,
# sessions end when an account is locked down, backups need the unlocked configuration login, and
# unencrypted connections are flagged. Needs a configured Dapper plus the demo directory.
set -u
BASE=${BASE:-http://localhost:8080}
CFGDN=${CFG_DN:-cn=admin,cn=config}; CFGPW=${CFG_PW:-config}
PASS=0; FAIL=0
J=$(mktemp -d)
req() { local jar=$1 m=$2 p=$3 d=${4:-}
  local out; out=$(curl -s -w '\n%{http_code}' -b "$J/$jar" -c "$J/$jar" -X "$m" -H 'X-Requested-With: dapper' -H 'Content-Type: application/json' ${d:+-d "$d"} "$BASE$p")
  CODE=${out##*$'\n'}; BODY=${out%$'\n'*}; }
jq_() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" <<<"$BODY"; }
check()  { if [ "$CODE" = "$2" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got $CODE, want $2): ${BODY:0:300}"; fi; }
expect() { if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got '$2', want '$3')"; fi; }
R=$RANDOM; U=sec$R; U2=sec2x$R; PW1="first-pass-$R"; PW2="second-pass-$R"
pdn() { echo "uid=$1,ou=people,dc=example,dc=org"; }
cleanup() { for u in $U $U2; do req adm DELETE "/api/admin/user?dn=$(pdn $u)" >/dev/null; done
  req adm POST /api/admin/group/members?dn=cn=dapper-admins,ou=groups,dc=example,dc=org >/dev/null 2>&1; rm -rf "$J"; }
trap cleanup EXIT
login() { req "$1" POST /api/login "{\"username\":\"$2\",\"password\":\"$3\"}"; }

login adm alice alicepass; check "admin sign-in" 200

# --- 1. the saved service account password must not follow a changed server ---
req adm GET /api/admin/settings; S=$BODY
mod() { python3 -c "import json,sys;d=json.loads(sys.argv[1])['settings'];exec(sys.argv[2]);print(json.dumps(d))" "$S" "$1"; }
req adm POST /api/admin/settings/check "$(mod "d['ldap']['url']='ldap://attacker.invalid:389'")"
expect "a changed address is NOT tried with the saved password" "$(jq_ "d['checks'][0]['message'].startswith('Enter the server address and the')")" True
req adm PUT /api/admin/settings "$(mod "d['ldap']['url']='ldap://attacker.invalid:389'")"; check "and can't be saved without typing the password" 400
req adm POST /api/admin/settings/check "$(mod "d['ldap']['bindDN']='cn=somebody,dc=example,dc=org'")"
expect "a changed account name isn't tried with the saved password either" "$(jq_ "d['ok']")" False
req adm POST /api/admin/settings/check "$(mod "d['password']['minLength']=9")"
expect "changing something else still keeps the saved password" "$(jq_ "d['ok']")" True
expect "an unencrypted connection is flagged" "$(jq_ "any(c['key']=='encryption' and c.get('warn') for c in d['checks'])")" True

# --- 2. sessions end when an account is locked down ---
mk() { req adm POST /api/admin/users "{\"username\":\"$1\",\"attrs\":{\"givenName\":[\"Se\"],\"sn\":[\"Cu\"]},\"password\":\"$2\"}" >/dev/null; }
mk $U "$PW1"
login a $U "$PW1"; req a GET /api/me; check "a signed-in person can use their session" 200
req adm POST "/api/admin/user/password?dn=$(pdn $U)" "{\"password\":\"$PW2\"}"; check "admin resets their password" 200
req a GET /api/me; check "the old session ends (an attacker's session doesn't survive a reset)" 401
login a $U "$PW2"; req a GET /api/me; check "signing in with the new password works" 200
req adm POST "/api/admin/user/suspend?dn=$(pdn $U)"; SDN=$(jq_ "d['dn']")
req a GET /api/me; check "suspending ends their session" 401
req adm POST "/api/admin/user/restore?dn=$SDN" >/dev/null
login a $U "$PW2"; req a GET /api/me; check "restored: sign-in works again" 200
req adm DELETE "/api/admin/user?dn=$(pdn $U)" >/dev/null
req a GET /api/me; check "deleting ends their session" 401

# own password change signs out the other devices, not this one
mk $U2 "$PW1"
login phone $U2 "$PW1"; login laptop $U2 "$PW1"
req laptop POST /api/me/password "{\"current\":\"$PW1\",\"new\":\"$PW2\"}"; check "change own password" 200
req phone GET /api/me; check "their other device is signed out" 401
req laptop GET /api/me; check "the device that changed it stays signed in" 200

# removing an administrator ends their admin session at once
GDN=cn=dapper-admins,ou=groups,dc=example,dc=org
req adm POST "/api/admin/group/members?dn=$GDN" "{\"member\":\"$(pdn $U2)\"}" >/dev/null
login boss $U2 "$PW2"; req boss GET /api/admin/users; check "a new administrator can use admin pages" 200
req adm DELETE "/api/admin/group/members?dn=$GDN&member=$(pdn $U2)"; check "an administrator is removed from the group" 200
req boss GET /api/admin/users; check "their admin session ends immediately" 401

# --- 3. configuration backups need the unlocked configuration login ---
req adm POST /api/admin/overlays/unlock "{\"dn\":\"$CFGDN\",\"password\":\"$CFGPW\"}"; check "unlock the server settings" 200
req adm POST /api/admin/overlays/apply '{"overlay":"refint","action":"update","values":{"attributes":"member memberOf"},"confirmed":true}'; check "make a change (saves a backup)" 200
BK=$(jq_ "d['backup']")
req adm GET "/api/admin/overlays/backup?name=$BK"; check "an unlocked administrator can download it" 200
LDAPC=${LDAP_CONTAINER:-dapper-openldap-1}
HAS=$(docker exec $LDAPC ldapsearch -Y EXTERNAL -H ldapi:/// -b 'olcDatabase={1}mdb,cn=config' -s base -LLL olcRootPW 2>/dev/null | grep -c '^olcRootPW:')
expect "(the server really does hold a root password hash)" "$HAS" 1
expect "and the backup leaves it out" "$(echo "$BODY" | grep -ciE '^(olcRootPW|userPassword|sambaNTPassword)::? ')" 0
req adm POST /api/admin/overlays/lock >/dev/null
req adm GET "/api/admin/overlays/backup?name=$BK"; check "once locked, a backup can't be downloaded" 403
req adm GET "/api/admin/overlays/backup?name=../../etc/passwd"; check "backup names can't be used to read other files" 403

# --- 4. the Activity record ---
MARK="wrong-$R-marker"
req t POST /api/login "{\"username\":\"nosuchperson$R\",\"password\":\"$MARK\"}"; check "a failed sign-in" 401
req t POST /api/login "{\"username\":\"=HYPERLINK(\\\"x\\\")$R\",\"password\":\"$MARK\"}" >/dev/null
req adm GET "/api/admin/audit?q=nosuchperson$R"; check "the activity list is available to administrators" 200
expect "a failed sign-in is recorded with the username typed" "$(jq_ "[e['action'] for e in d['events']]")" "['login.failed']"
expect "and where it came from" "$(jq_ "'ip' in d['events'][0]['detail']")" True
expect "but the password is never recorded" "$(req adm GET "/api/admin/audit?pageSize=500"; echo "$BODY" | grep -c "$MARK")" 0
req adm GET "/api/admin/audit?category=login&pageSize=500"
expect "the category filter works" "$(jq_ "all(e['action'].startswith('login') for e in d['events']) and len(d['events'])>0")" True
req adm GET "/api/admin/audit?q=$U2"
expect "account changes are recorded with who did them" "$(jq_ "any(e['action']=='user.create' and 'alice' in e['actor'] for e in d['events'])")" True
req bob POST /api/login '{"username":"bob","password":"bobpass"}' >/dev/null
req bob GET /api/admin/audit; check "ordinary people can't see the activity record" 403
req adm GET "/api/admin/audit?format=csv&pageSize=500"; check "download as CSV" 200
expect "CSV starts with a header row" "$(echo "$BODY" | head -1)" "time,who,action,target,details"
expect "spreadsheet formulas in the data are neutralised" "$([ "$(echo "$BODY" | grep -c "login.failed,\"\?'=HYPERLINK")" -ge 1 ] && echo yes)" yes
expect "and none is left raw" "$(echo "$BODY" | grep -c "login.failed,\"\?=HYPERLINK")" 0

echo; echo "passed $PASS, failed $FAIL"
[ "$FAIL" = 0 ]
