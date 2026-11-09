#!/usr/bin/env bash
# Setup of a Dapper-run directory server, end to end through Dapper's API. Needs an UNCONFIGURED Dapper started
# with docker-compose.server.yml (a fresh stack: `docker compose ... down -v` first). Usage:
#   SETUP_CODE=<code> scripts/e2e-bundled.sh [base-url]
set -u
BASE=${1:-http://localhost:8080}
: "${SETUP_CODE:?set SETUP_CODE to the code from the Dapper log}"
PASS=0; FAIL=0
J=$(mktemp -d); trap 'rm -rf "$J"' EXIT
req() { # req <jar> <method> <path> [json]
  local out; out=$(curl -s -w '\n%{http_code}' -b "$J/$1" -c "$J/$1" -X "$2" -H 'X-Requested-With: dapper' \
    -H "X-Setup-Code: $SETUP_CODE" -H 'Content-Type: application/json' ${4:+-d "$4"} "$BASE$3")
  CODE=${out##*$'\n'}; BODY=${out%$'\n'*}
}
jq_() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" <<<"$BODY"; }
expect() { if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got '$2', want '$3') $BODY" | head -c 400; echo; fi; }

REQ='{"organization":"Test Org","domain":"test.lan","admin":{"username":"alice","givenName":"Alice","sn":"Admin","password":"correct horse battery"},"features":{"memberOf":true,"refint":true},"settings":{"ldap":{},"users":{"loginAttributes":["uid","mail"],"objectClasses":["inetOrgPerson"],"rdnAttribute":"uid"},"groups":{},"admin":{},"password":{"minLength":8},"session":{"idleMinutes":30,"maxHours":12},"unix":{"enabled":true,"uidMin":10000,"uidMax":60000,"homePattern":"/home/%u","defaultShell":"/bin/bash","shadow":false,"sshKeys":true}}}'

req a GET /api/setup/bundled/status;  expect "server found and reachable" "$(jq_ "d['found'], d.get('reachable')")" "(True, True)"
req a POST /api/setup/bundled/preview "${REQ/test.lan/bad}"; expect "bad domain refused" "$CODE" 400
req a POST /api/setup/bundled/preview "$REQ"; expect "preview names the directory" "$(jq_ "d['suffix']")" "dc=test,dc=lan"
req a POST /api/setup/bundled/apply "$REQ"; expect "apply without approval refused" "$CODE" 400
req a POST /api/setup/bundled/apply "${REQ%\}}"',"confirmed":true}'; expect "apply succeeds" "$CODE" 200
MGR=$(jq_ "d['managerPassword']"); CFG=$(jq_ "d['configPassword']")
expect "configuration password is returned once" "$([ -n "$CFG" ] && echo yes)" yes
req a GET /api/setup/bundled/status; expect "hand-off removed after setup" "$CODE" 403   # Dapper is configured now: setup is closed
req a GET /api/setup/status; expect "Dapper is configured" "$(jq_ "d['configured']")" True
req b POST /api/login '{"username":"alice","password":"correct horse battery"}'; expect "first administrator signs in" "$CODE" 200
expect "and is an administrator" "$(jq_ "d['isAdmin']")" True
req b GET /api/admin/users; expect "people list works" "$CODE" 200
echo; echo "passed $PASS, failed $FAIL"; [ "$FAIL" = 0 ]
