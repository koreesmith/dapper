#!/usr/bin/env bash
# End-to-end tests for the rest of the Server page: schemas, indexes, access rules, TLS, replication, and the
# last-sign-in and replication overlays. Needs a configured Dapper plus the demo directory (cn=config admin
# cn=admin,cn=config / config). Leaves the server as it found it.
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
cleanup() { req adm POST /api/admin/server/apply '{"kind":"index.remove","attr":"roomNumber","confirmed":true}' >/dev/null; rm -rf "$J"; }
trap cleanup EXIT

req adm POST /api/login '{"username":"alice","password":"alicepass"}'; check "admin sign-in" 200
req bob POST /api/login '{"username":"bob","password":"bobpass"}'
req bob GET /api/admin/server/config; check "server settings are admin-only" 403
req bob POST /api/admin/server/apply '{"kind":"index.add","attr":"l","types":["eq"],"confirmed":true}'; check "changes are admin-only" 403
req adm GET /api/admin/server/config; check "locked until the configuration login" 409
req adm POST /api/admin/overlays/unlock "{\"dn\":\"$CFGDN\",\"password\":\"$CFGPW\"}"; check "unlock" 200

# --- reading ---
req adm GET /api/admin/server/config; check "read the server settings" 200
expect "core schema is loaded" "$(jq_ "'core' in d['schemas']")" True
expect "indexes include objectClass" "$(jq_ "'objectClass' in [a for i in d['indexes'] for a in i['attrs']]")" True
expect "access rules are listed" "$(jq_ "len(d['acl']) > 0")" True
expect "the served certificate (or why it can't be read) is reported" "$(jq_ "'served' in d['tls'] or 'servedError' in d['tls']")" True
expect "replication facts are arrays" "$(jq_ "isinstance(d['replication']['syncrepl'], list) and isinstance(d['replication']['serverID'], list)")" True
expect "the directory manager is named" "$(jq_ "d['rootDN'] != ''")" True
SSH=$(jq_ "d['sshKeysSchema']")

# --- schema ---
req adm POST /api/admin/server/preview '{"kind":"schema.sshkeys"}'
if [ "$SSH" = True ]; then check "SSH key schema already loaded: nothing to add" 400
else check "preview adding the SSH key schema" 200; expect "preview is LDIF" "$(jq_ "'cn=openssh-lpk,cn=schema,cn=config' in d['ldif']")" True; fi
req adm POST /api/admin/server/apply '{"kind":"schema.sshkeys"}'; check "must be approved to apply" 400

# --- indexes ---
req adm POST /api/admin/server/preview '{"kind":"index.add","attr":"roomNumber","types":["eq","sub"]}'; check "preview adding an index" 200
expect "the preview shows what it would add" "$(jq_ "'olcDbIndex: roomNumber eq,sub' in d['ldif']")" True
expect "nothing changed by a preview" "$(cfgsearch '(objectClass=olcMdbConfig)' olcDbIndex | grep -c roomNumber)" 0
req adm POST /api/admin/server/preview '{"kind":"index.add","attr":"bad name","types":["eq"]}'; check "bad attribute name refused" 400
req adm POST /api/admin/server/preview '{"kind":"index.add","attr":"roomNumber","types":["fuzzy"]}'; check "unknown index type refused" 400
req adm POST /api/admin/server/preview '{"kind":"index.add","attr":"roomNumber","types":[]}'; check "an index needs a type" 400
req adm POST /api/admin/server/preview '{"kind":"index.remove","attr":"objectClass"}'; check "the server's own index can't be removed" 400
req adm POST /api/admin/server/apply '{"kind":"index.add","attr":"roomNumber","types":["eq","sub"],"confirmed":true}'; check "add an index" 200
BK=$(jq_ "d['backup']"); expect "a backup was saved" "$(echo $BK | grep -c '^cn-config-')" 1
expect "the server has the index" "$(cfgsearch '(objectClass=olcMdbConfig)' olcDbIndex | grep -c 'roomNumber eq,sub')" 1
req adm POST /api/admin/server/apply '{"kind":"index.add","attr":"roomNumber","types":["eq"],"confirmed":true}'; check "indexing the same attribute twice is refused" 400
req adm GET /api/admin/server/config
expect "the new index is listed" "$(jq_ "[i['raw'] for i in d['indexes'] if 'roomNumber' in i['attrs']]")" "['roomNumber eq,sub']"
req adm POST /api/admin/server/apply '{"kind":"index.remove","attr":"roomNumber","confirmed":true}'; check "remove the index" 200
expect "the server no longer has it" "$(cfgsearch '(objectClass=olcMdbConfig)' olcDbIndex | grep -c 'roomNumber')" 0

# --- access rules ---
req adm GET /api/admin/server/config
RULES=$(jq_ "__import__('json').dumps(d['acl'])")
req adm POST /api/admin/server/preview '{"kind":"acl.set","rules":[]}'; check "no rules is refused" 400
req adm POST /api/admin/server/preview '{"kind":"acl.set","rules":["grant everything"]}'; check "nonsense is refused" 400
req adm POST /api/admin/server/preview '{"kind":"acl.set","rules":["to * by * read\nto attrs=userPassword by * write"]}'; check "a rule can't smuggle in a second line" 400
req adm POST /api/admin/server/preview '{"kind":"acl.set","rules":["to * by * read"]}'; check "a weak rule set can be previewed" 200
expect "and is warned about" "$(jq_ "len(d['warnings']) >= 2")" True
req adm POST /api/admin/server/apply "{\"kind\":\"acl.set\",\"rules\":$RULES,\"confirmed\":true}"; check "re-apply the current rules unchanged" 200
req adm GET /api/admin/server/config
expect "the rules are the same afterwards" "$(jq_ "__import__('json').dumps(d['acl'])")" "$RULES"

# --- TLS ---
req adm POST /api/admin/server/preview '{"kind":"tls.set","cert":"/etc/ssl/x.crt","key":""}'; check "certificate and key go together" 400
req adm POST /api/admin/server/preview '{"kind":"tls.set","cert":"relative/x.crt","key":"/etc/ssl/x.key"}'; check "paths must be absolute" 400
req adm POST /api/admin/server/preview '{"kind":"tls.set","cert":"/etc/ssl/../x.crt","key":"/etc/ssl/x.key"}'; check "paths can't climb out" 400
req adm POST /api/admin/server/preview '{"kind":"tls.set","cert":"/etc/ssl/new.crt","key":"/etc/ssl/new.key"}'; check "preview new certificate paths" 200
expect "with a warning about the files" "$(jq_ "any('exist' in w for w in d['warnings'])")" True

# --- overlays that arrived with this page ---
req adm GET /api/admin/overlays
expect "last sign-in and replication overlays are offered" "$(jq_ "sorted(o['id'] for o in d['overlays'] if o['id'] in ('lastbind','syncprov'))")" "['lastbind', 'syncprov']"
req adm POST /api/admin/overlays/preview '{"overlay":"syncprov","action":"enable","values":{}}'; check "preview the replication overlay" 200
expect "it loads the module and adds the overlay" "$(jq_ "'olcSyncProvConfig' in d['ldif'] and 'olcSpCheckpoint: 100 10' in d['ldif']")" True
req adm POST /api/admin/overlays/preview '{"overlay":"syncprov","action":"enable","values":{"sessionlog":"-5"}}'; check "bad numbers are refused" 400
req adm POST /api/admin/overlays/preview '{"overlay":"lastbind","action":"enable","values":{"precision":"600"}}'; check "preview last sign-in recording" 200
expect "with its precision" "$(jq_ "'olcLastBindPrecision: 600' in d['ldif']")" True

req adm POST /api/admin/overlays/lock >/dev/null
req adm GET /api/admin/server/config; check "locking again hides it" 409

echo; echo "$PASS passed, $FAIL failed"; [ "$FAIL" = 0 ]
