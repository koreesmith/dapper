#!/usr/bin/env bash
# End-to-end tests for overlay management against the demo OpenLDAP (its cn=config admin is
# cn=admin,cn=config / config). Needs a configured Dapper plus the demo directory, and docker
# access to the demo container to check what the server really does. Leaves overlays as it found them
# (memberOf and refint on; the rest off).
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
check()  { if [ "$CODE" = "$2" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got $CODE, want $2): ${BODY:0:400}"; fi; }
expect() { if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got '$2', want '$3')"; fi; }
cfgsearch() { docker exec $LDAPC ldapsearch -Y EXTERNAL -H ldapi:/// -b cn=config -LLL "$@" 2>/dev/null; }
overlay_on() { cfgsearch "(objectClass=olcOverlayConfig)" dn | grep -ci "^dn: olcOverlay={[0-9]*}$1,"; }
apply() { req adm POST /api/admin/overlays/apply "{\"overlay\":\"$1\",\"action\":\"$2\",\"values\":$3,\"confirmed\":true${4:-}}"; }
state() { req adm GET /api/admin/overlays; jq_ "[o for o in d['overlays'] if o['id']=='$1'][0]['enabled']"; }
LAST=0
cleanup() { for n in ovl1 ovl2 ovl3; do req adm DELETE "/api/admin/user?dn=uid=$n,ou=people,dc=example,dc=org" >/dev/null; done; rm -rf "$J"; }
trap cleanup EXIT

req adm POST /api/login '{"username":"alice","password":"alicepass"}'; check "admin sign-in" 200
req bob POST /api/login '{"username":"bob","password":"bobpass"}'; check "user sign-in" 200
req bob GET /api/admin/overlays; check "overlays are admin-only" 403

# --- baseline: unique/auditlog/ppolicy off, refint on, whatever an earlier run left ---
# (OpenLDAP 2.4 can't remove an overlay while running, so "off" means paused: its key setting is cleared.)
req adm POST /api/admin/overlays/unlock "{\"dn\":\"$CFGDN\",\"password\":\"$CFGPW\"}"; check "unlock to set the baseline" 200
for o in unique auditlog ppolicy; do [ "$(state $o)" = True ] && apply $o disable '{}' >/dev/null; done
[ "$(state refint)" = False ] && apply refint enable '{}' >/dev/null
# memberOf as the demo server ships it: watching groupOfUniqueNames, not the groupOfNames Dapper manages
apply memberof update '{"groupOC":"groupOfUniqueNames","memberAD":"uniqueMember"}' >/dev/null
docker exec $LDAPC ldapdelete -x -H ldap://localhost -D cn=admin,dc=example,dc=org -w admin cn=default,ou=policies,dc=example,dc=org ou=policies,dc=example,dc=org >/dev/null 2>&1
req adm POST /api/admin/overlays/lock >/dev/null
cfgval() { cfgsearch "(objectClass=$1)" "$2" | grep -c "^$2:"; }

# --- locked: nothing is known, but the catalog is there ---
req adm GET /api/admin/overlays
expect "locked: catalog lists the overlays" "$(jq_ "sorted(o['id'] for o in d['overlays'])")" "['auditlog', 'lastbind', 'memberof', 'ppolicy', 'refint', 'syncprov', 'unique']"
expect "locked: state is unknown, not guessed" "$(jq_ "d['unlocked']==False and all(o['enabled'] is None for o in d['overlays'])")" True
req adm POST /api/admin/overlays/preview '{"overlay":"auditlog","action":"enable","values":{"file":"/tmp/x.ldif"}}'
check "locked: preview still works (commands to run by hand)" 200
expect "locked: gives LDIF and a command" "$(jq_ "'olcAuditlogFile: /tmp/x.ldif' in d['ldif'] and 'ldapmodify' in d['commands']['config']")" True
apply auditlog enable '{"file":"/tmp/x.ldif"}'; check "locked: applying is refused" 409
req adm POST /api/admin/overlays/unlock "{\"dn\":\"$CFGDN\",\"password\":\"wrong\"}"; check "wrong configuration password refused" 401
req adm POST /api/admin/overlays/unlock "{\"dn\":\"$CFGDN\",\"password\":\"$CFGPW\"}"; check "unlock" 200

# --- reading state ---
req adm GET /api/admin/overlays
expect "unlocked: database identified" "$(jq_ "d['database']")" "olcDatabase={1}mdb,cn=config"
expect "memberOf is on (as the demo server ships)" "$(jq_ "[o for o in d['overlays'] if o['id']=='memberof'][0]['enabled']")" True
expect "ppolicy is off" "$(jq_ "[o for o in d['overlays'] if o['id']=='ppolicy'][0]['enabled']")" False
expect "a settings mismatch is pointed out (memberOf watches a different group class than Dapper's)" "$(jq_ "len([o for o in d['overlays'] if o['id']=='memberof'][0].get('notes',[]))")" 1

# --- memberOf: it can't be switched off, but it can be made to match Dapper's groups ---
apply memberof disable '{}'; check "memberOf can't be switched off (explained, not attempted)" 400
expect "and the explanation says how" "$(jq_ "'restart' in d['error']")" True
req adm POST /api/admin/overlays/preview '{"overlay":"memberof","action":"update","values":{"groupOC":"groupOfNames","memberAD":"member"}}'
check "preview a change to memberOf" 200
req adm POST /api/admin/overlays/apply '{"overlay":"memberof","action":"update","values":{"groupOC":"groupOfNames","memberAD":"member"},"confirmed":false}'; check "applying without approval is refused" 400
apply memberof update '{"groupOC":"groupOfNames","memberAD":"member"}'; check "make memberOf match Dapper's groups" 200
BK=$(jq_ "d['backup']")
req adm GET "/api/admin/overlays/backup?name=$BK"; check "the backup can be downloaded" 200
expect "and holds the configuration from before the change" "$(echo "$BODY" | grep -c 'olcOverlay: {0}memberof')" 1
req adm GET /api/admin/overlays
expect "the mismatch warning is gone" "$(jq_ "len([o for o in d['overlays'] if o['id']=='memberof'][0].get('notes',[]))")" 0
req adm POST /api/admin/overlays/memberof/refresh '{"confirmed":false}'; check "refreshing groups needs approval" 400
req adm POST /api/admin/overlays/memberof/refresh '{"confirmed":true}'; check "refresh existing groups" 200
expect "groups were refreshed" "$(jq_ "d['refreshedGroups']>=2")" True
MO=$(docker exec $LDAPC ldapsearch -x -H ldap://localhost -D cn=admin,dc=example,dc=org -w admin -b uid=bob,ou=people,dc=example,dc=org -LLL memberOf 2>/dev/null | grep -c '^memberOf: cn=staff')
expect "existing members now show memberOf" "$MO" 1
apply memberof enable '{}'; check "turning on twice is refused" 400

# --- refint: pause and resume ---
apply refint disable '{}'; check "turn refint off (paused)" 200
expect "refint's settings are cleared" "$(cfgval olcRefintConfig olcRefintAttribute)" 0
expect "the state says off" "$(state refint)" False
apply refint disable '{}'; check "turning off twice is refused" 400
apply refint enable '{"attributes":"member memberOf"}'; check "turn refint back on" 200
expect "refint is configured again (one attribute name per value)" "$(cfgval olcRefintConfig olcRefintAttribute)" 2
expect "refint keeps Dapper's placeholder for emptied groups" "$(cfgsearch "(objectClass=olcRefintConfig)" olcRefintNothing | grep -c 'dapper-placeholder')" 1
apply refint update '{"attributes":"member; drop"}'; check "bad attribute names are rejected" 400

# --- unique ---
req adm POST /api/admin/users '{"username":"ovl1","attrs":{"givenName":["Ov"],"sn":["One"],"mail":["shared@example.org"]}}' >/dev/null
req adm POST /api/admin/users '{"username":"ovl2","attrs":{"givenName":["Ov"],"sn":["Two"],"mail":["shared@example.org"]}}' >/dev/null
req adm POST /api/admin/overlays/preview '{"overlay":"unique","action":"enable","values":{"attrs":"uid,mail"}}'
expect "existing duplicates are reported before enabling" "$(jq_ "any('shared@example.org' in w for w in d['warnings'])")" True
apply unique enable '{"attrs":"uid,mail"}'; check "turn unique on (module loaded on demand if needed)" 200
expect "unique is configured on the server (one rule per attribute)" "$(cfgval olcUniqueConfig olcUniqueURI)" 2
expect "the module is loaded" "$(cfgsearch "(objectClass=olcModuleList)" olcModuleLoad | grep -ci 'unique')" 1
req adm POST /api/admin/users '{"username":"ovl3","attrs":{"givenName":["Ov"],"sn":["Three"],"mail":["fresh@example.org"]}}'; check "a new unique email is fine" 200
req adm PATCH "/api/admin/user?dn=uid=ovl3,ou=people,dc=example,dc=org" '{"attrs":{"mail":["fresh@example.org","bob@example.org"]}}'
check "reusing someone else's email is refused" 409
expect "with a plain-language reason" "$(jq_ "'already uses' in d['error']")" True
apply unique update '{"attrs":"uid"}'; check "narrow it to usernames only" 200
req adm PATCH "/api/admin/user?dn=uid=ovl3,ou=people,dc=example,dc=org" '{"attrs":{"mail":["fresh@example.org","bob@example.org"]}}'
check "now the shared email is allowed" 200
apply unique disable '{}'; check "turn unique off (paused)" 200
expect "its URIs are cleared" "$(cfgval olcUniqueConfig olcUniqueURI)" 0
req adm PATCH "/api/admin/user?dn=uid=ovl2,ou=people,dc=example,dc=org" '{"attrs":{"mail":["bob@example.org"]}}'; check "and duplicates are accepted again" 200

# --- auditlog ---
docker exec $LDAPC rm -f /tmp/dapper-audit.ldif
apply auditlog enable '{"file":"/tmp/dapper-audit.ldif"}'; check "turn the change log on" 200
req adm PATCH "/api/admin/user?dn=uid=ovl3,ou=people,dc=example,dc=org" '{"attrs":{"title":["Logged"]}}' >/dev/null
expect "a change shows up in the log file" "$(docker exec $LDAPC grep -c 'Logged' /tmp/dapper-audit.ldif 2>/dev/null)" 1
apply auditlog enable '{"file":"/tmp/other.ldif"}'; check "enabling again is refused" 400
apply auditlog update '{"file":"relative.ldif"}'; check "a relative log path is rejected" 400
apply auditlog disable '{}'; check "turn the change log off (paused)" 200
docker exec $LDAPC rm -f /tmp/dapper-audit.ldif
req adm PATCH "/api/admin/user?dn=uid=ovl3,ou=people,dc=example,dc=org" '{"attrs":{"title":["NotLogged"]}}' >/dev/null
expect "after turning it off, changes are no longer logged" "$(docker exec $LDAPC sh -c 'grep -c NotLogged /tmp/dapper-audit.ldif 2>/dev/null || echo 0' | tail -1)" 0

# --- ppolicy: password rules and lockout, tested for real ---
apply ppolicy enable '{"minLength":"6","lockout":"true","maxFailures":"3","lockoutMinutes":"0","policyDN":"cn=default,ou=policies,dc=example,dc=org"}'
check "turn password rules on" 200
expect "the policy entry was created in the directory" "$(docker exec $LDAPC ldapsearch -x -H ldap://localhost -D cn=admin,dc=example,dc=org -w admin -b cn=default,ou=policies,dc=example,dc=org -s base -LLL pwdMaxFailure 2>/dev/null | grep -c 'pwdMaxFailure: 3')" 1
req adm GET /api/admin/overlays
expect "the form reads the policy back" "$(jq_ "[o for o in d['overlays'] if o['id']=='ppolicy'][0]['values']['maxFailures']")" 3
req adm POST "/api/admin/user/password?dn=uid=ovl1,ou=people,dc=example,dc=org" '{"password":"correct-horse-1"}' >/dev/null
for i in 1 2 3; do req t POST /api/login '{"username":"ovl1","password":"wrong-guess"}'; done
req t POST /api/login '{"username":"ovl1","password":"correct-horse-1"}'; check "the account is locked after 3 wrong passwords" 401
req adm GET "/api/admin/users?q=ovl1"
expect "the People list shows it as locked" "$(jq_ "d['users'][0]['suspended']")" True
req adm POST "/api/admin/user/restore?dn=uid=ovl1,ou=people,dc=example,dc=org"; check "an admin can unlock it" 200
req t2 POST /api/login '{"username":"ovl1","password":"correct-horse-1"}'; check "and they can sign in again" 200
apply ppolicy update '{"minLength":"12","lockout":"false","maxAgeDays":"0","policyDN":"cn=default,ou=policies,dc=example,dc=org"}'; check "change the password rules" 200
expect "the change reached the policy entry" "$(docker exec $LDAPC ldapsearch -x -H ldap://localhost -D cn=admin,dc=example,dc=org -w admin -b cn=default,ou=policies,dc=example,dc=org -s base -LLL pwdMinLength pwdLockout 2>/dev/null | grep -v '^dn:' | grep -v '^$' | sort | tr '\n' ' ' | sed 's/ *$//')" "pwdLockout: FALSE pwdMinLength: 12"
apply ppolicy update '{"lockout":"true","maxFailures":"3","lockoutMinutes":"0","policyDN":"cn=default,ou=policies,dc=example,dc=org"}' >/dev/null
apply ppolicy disable '{}'; check "turn password rules off (paused)" 200
expect "no default policy is set any more" "$(cfgval olcPPolicyConfig olcPPolicyDefault)" 0
for i in 1 2 3 4; do req t POST /api/login '{"username":"ovl1","password":"wrong-guess"}'; done
req t3 POST /api/login '{"username":"ovl1","password":"correct-horse-1"}'; check "with the rules off, repeated wrong passwords no longer lock anyone" 200
docker exec $LDAPC ldapdelete -x -H ldap://localhost -D cn=admin,dc=example,dc=org -w admin cn=default,ou=policies,dc=example,dc=org ou=policies,dc=example,dc=org >/dev/null 2>&1

# --- other rules ---
apply nonsense enable '{}'; check "unknown overlay refused" 400
apply refint enable '{}' ''; check "enabling something that's on is refused" 400
req adm POST /api/admin/overlays/lock; check "lock again" 200
expect "locked: state is hidden again" "$(state memberof)" None

echo; echo "passed $PASS, failed $FAIL"
[ "$FAIL" = 0 ]
