#!/usr/bin/env bash
# End-to-end tests for password rules: the directory's rules apply to people changing their own password,
# the rules are shown to them in plain language, and the server's complaints are translated. Needs a configured
# Dapper plus the demo directory, and docker access to it (to clean up). Leaves password rules switched off.
set -u
BASE=${BASE:-http://localhost:8080}
LDAPC=${LDAP_CONTAINER:-dapper-openldap-1}
CFGDN=${CFG_DN:-cn=admin,cn=config}; CFGPW=${CFG_PW:-config}
PASS=0; FAIL=0
J=$(mktemp -d)
req() { local jar=$1 m=$2 p=$3 d=${4:-}
  local out; out=$(curl -s -w '\n%{http_code}' -b "$J/$jar" -c "$J/$jar" -X "$m" -H 'X-Requested-With: dapper' -H 'Content-Type: application/json' ${d:+-d "$d"} "$BASE$p")
  CODE=${out##*$'\n'}; BODY=${out%$'\n'*}; }
jq_() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" <<<"$BODY"; }
check()  { if [ "$CODE" = "$2" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got $CODE, want $2): ${BODY:0:300}"; fi; }
expect() { if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got '$2', want '$3')"; fi; }
R=$RANDOM; U=pw$R; P1="first-long-pass-$R"; P2="second-long-pass-$R"
pdn() { echo "uid=$1,ou=people,dc=example,dc=org"; }
PDN=cn=default,ou=policies,dc=example,dc=org
rules_off() { req adm POST /api/admin/overlays/unlock "{\"dn\":\"$CFGDN\",\"password\":\"$CFGPW\"}" >/dev/null
  req adm GET /api/admin/overlays; [ "$(jq_ "[o for o in d['overlays'] if o['id']=='ppolicy'][0]['enabled']")" = True ] && \
    req adm POST /api/admin/overlays/apply '{"overlay":"ppolicy","action":"disable","values":{},"confirmed":true}' >/dev/null
  docker exec $LDAPC ldapdelete -x -H ldap://localhost -D cn=admin,dc=example,dc=org -w admin $PDN ou=policies,dc=example,dc=org >/dev/null 2>&1; }
cleanup() { req adm DELETE "/api/admin/user?dn=$(pdn $U)" >/dev/null; rules_off; rm -rf "$J"; }
trap cleanup EXIT

req adm POST /api/login '{"username":"alice","password":"alicepass"}'; check "admin sign-in" 200
rules_off

# --- without directory rules, people see only Dapper's own minimum ---
req adm POST /api/admin/users "{\"username\":\"$U\",\"attrs\":{\"givenName\":[\"Pw\"],\"sn\":[\"Test\"]},\"password\":\"$P1\"}" >/dev/null
req u POST /api/login "{\"username\":\"$U\",\"password\":\"$P1\"}"; check "person signs in" 200
req u GET /api/me/password-policy; check "the rules endpoint answers" 200
expect "no directory rules: only Dapper's minimum applies" "$(jq_ "d['enforced']==False and d['minLength']==8")" True
req u POST /api/me/password "{\"current\":\"$P1\",\"new\":\"short\"}"; check "Dapper's own minimum still applies" 400
expect "and the message names the number" "$(jq_ "'at least 8' in d['error']")" True

# --- turn rules on ---
req adm POST /api/admin/overlays/unlock "{\"dn\":\"$CFGDN\",\"password\":\"$CFGPW\"}" >/dev/null
req adm POST /api/admin/overlays/apply "{\"overlay\":\"ppolicy\",\"action\":\"enable\",\"values\":{\"minLength\":\"12\",\"history\":\"2\",\"maxAgeDays\":\"30\",\"lockout\":\"false\",\"policyDN\":\"$PDN\"},\"confirmed\":true}"
check "turn password rules on" 200
req adm GET /api/admin/settings
expect "Dapper remembers where the rules live" "$(jq_ "d['settings']['password']['policyDN']")" "$PDN"
expect "the policy entry asks the server to check passwords" "$(docker exec $LDAPC ldapsearch -x -H ldap://localhost -D cn=admin,dc=example,dc=org -w admin -b $PDN -s base -LLL pwdCheckQuality 2>/dev/null | grep -c 'pwdCheckQuality: 1')" 1

# a person created after the rules, so the server stamps when their password was set
req adm POST "/api/admin/user/password?dn=$(pdn $U)" "{\"password\":\"$P1\"}" >/dev/null
req u POST /api/login "{\"username\":\"$U\",\"password\":\"$P1\"}"; check "sign in again" 200
req u GET /api/me/password-policy
expect "people are told the directory's rules" "$(jq_ "d['enforced'] and d['minLength']==12 and d['history']==2 and d['maxAgeDays']==30")" True
expect "and when their password expires" "$(jq_ "d['daysLeft'] is not None and 28<=d['daysLeft']<=30 and d['expiresOn']!=''")" True

# --- the directory's rules apply when people change their own password ---
req u POST /api/me/password "{\"current\":\"$P1\",\"new\":\"tooshort9\"}"; check "a password that breaks the directory's rules is refused" 400
expect "in plain language, not LDAP-speak" "$(jq_ "'password rules' in d['error'] and 'LDAP' not in d['error']")" True
req u POST /api/me/password "{\"current\":\"$P1\",\"new\":\"$P2\"}"; check "a password that meets them works" 200
req u POST /api/login "{\"username\":\"$U\",\"password\":\"$P1\"}" >/dev/null
req u2 POST /api/login "{\"username\":\"$U\",\"password\":\"$P2\"}"; check "sign in with the new password" 200
req u2 POST /api/me/password "{\"current\":\"$P2\",\"new\":\"$P1\"}"; check "going back to a recent password is refused" 400
expect "with a clear reason" "$(jq_ "'before' in d['error']")" True

# --- administrators can still reset to something specific ---
req adm POST "/api/admin/user/password?dn=$(pdn $U)" '{"password":"admin-chosen-1"}'; check "an administrator's reset isn't blocked by history" 200

# --- turn rules off: people are no longer told rules that aren't in force ---
req adm POST /api/admin/overlays/apply '{"overlay":"ppolicy","action":"disable","values":{},"confirmed":true}'; check "turn password rules off" 200
req adm GET /api/admin/settings
expect "Dapper forgets the rules' location" "$(jq_ "d['settings']['password']['policyDN']")" ""
req u3 POST /api/login "{\"username\":\"$U\",\"password\":\"admin-chosen-1\"}"
req u3 GET /api/me/password-policy
expect "people are no longer shown rules that aren't enforced" "$(jq_ "d['enforced']")" False

echo; echo "passed $PASS, failed $FAIL"
[ "$FAIL" = 0 ]
