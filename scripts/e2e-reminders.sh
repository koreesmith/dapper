#!/usr/bin/env bash
# End-to-end tests for password expiry reminders: nothing without password rules, one email per reminder point,
# no repeats, nothing for locked or address-less people, and the administrator's "Check now". Needs a configured Dapper
# on this machine plus the demo directory, and docker access to it. Starts its own tiny mail server. Leaves password
# rules and email switched off.
set -u
BASE=${BASE:-http://localhost:8080}
LDAPC=${LDAP_CONTAINER:-dapper-openldap-1}
CFGDN=${CFG_DN:-cn=admin,cn=config}; CFGPW=${CFG_PW:-config}
MAIL_PORT=${MAIL_PORT:-2525}
PASS=0; FAIL=0
J=$(mktemp -d)
req() { local jar=$1 m=$2 p=$3 d=${4:-}
  local out; out=$(curl -s -w '\n%{http_code}' -b "$J/$jar" -c "$J/$jar" -X "$m" -H 'X-Requested-With: dapper' -H 'Content-Type: application/json' ${d:+-d "$d"} "$BASE$p")
  CODE=${out##*$'\n'}; BODY=${out%$'\n'*}; }
jq_() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" <<<"$BODY"; }
check()  { if [ "$CODE" = "$2" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got $CODE, want $2): ${BODY:0:300}"; fi; }
expect() { if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got '$2', want '$3')"; fi; }

cat > "$J/sink.py" <<'PY'
import socket, sys
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", int(sys.argv[2]))); s.listen(5)
while True:
    c, _ = s.accept(); f = c.makefile("rwb"); w = lambda x: (f.write((x + "\r\n").encode()), f.flush())
    w("220 sink"); data = False; msg = []
    while True:
        l = f.readline()
        if not l: break
        l = l.decode().rstrip("\r\n")
        if data:
            if l == ".": data = False; open(sys.argv[1], "a").write("\n".join(msg) + "\n=====\n"); w("250 ok"); continue
            msg.append(l); continue
        if l.startswith("EHLO"): w("250 sink")
        elif l == "DATA": data = True; w("354 go")
        elif l == "QUIT": w("221 bye"); break
        else: w("250 ok")
    c.close()
PY
LOG=$J/mail.log; : > "$LOG"
python3 "$J/sink.py" "$LOG" "$MAIL_PORT" & SINK=$!
R=$RANDOM; U=ex$R; N=en$R; P1="first-long-pass-$R"
pdn() { echo "uid=$1,ou=people,dc=example,dc=org"; }
PDN=cn=default,ou=policies,dc=example,dc=org
unlock() { req adm POST /api/admin/overlays/unlock "{\"dn\":\"$CFGDN\",\"password\":\"$CFGPW\"}" >/dev/null; }
rules_off() { unlock
  req adm GET /api/admin/overlays; [ "$(jq_ "[o for o in d['overlays'] if o['id']=='ppolicy'][0]['enabled']")" = True ] && \
    req adm POST /api/admin/overlays/apply '{"overlay":"ppolicy","action":"disable","values":{},"confirmed":true}' >/dev/null
  docker exec $LDAPC ldapdelete -x -H ldap://localhost -D cn=admin,dc=example,dc=org -w admin $PDN ou=policies,dc=example,dc=org >/dev/null 2>&1; }
cleanup() { req adm DELETE "/api/admin/user?dn=$(pdn $U)" >/dev/null; req adm DELETE "/api/admin/user?dn=$(pdn $N)" >/dev/null
  req adm PUT /api/admin/mail '{"enabled":false,"port":587,"security":"starttls"}' >/dev/null; rules_off; kill $SINK 2>/dev/null; rm -rf "$J"; }
trap cleanup EXIT
count() { grep -c '^=====$' "$LOG"; }
waitmail() { for _ in $(seq 1 40); do [ "$(count)" -ge "$1" ] && return 0; sleep 0.25; done; return 1; }
last() { python3 - "$LOG" <<'PY'
import sys; t = open(sys.argv[1]).read().split("=====\n"); print([m for m in t if m.strip()][-1])
PY
}
maxage() { docker exec -i $LDAPC ldapmodify -x -H ldap://localhost -D cn=admin,dc=example,dc=org -w admin >/dev/null <<LDIF
dn: $PDN
changetype: modify
replace: pwdMaxAge
pwdMaxAge: $1
LDIF
}

req adm POST /api/login '{"username":"alice","password":"alicepass"}'; check "admin sign-in" 200
rules_off
req adm PUT /api/admin/mail "{\"enabled\":true,\"remindExpiry\":true,\"host\":\"127.0.0.1\",\"port\":$MAIL_PORT,\"security\":\"none\",\"from\":\"dapper@example.org\",\"publicURL\":\"$BASE\"}"; check "email turned on" 200

# --- nothing to remind about without password rules ---
req adm GET /api/admin/mail; expect "the page says rules are off" "$(jq_ "d['reminders']['rulesOn']")" False
req adm POST /api/admin/mail/reminders; check "'Check now' explains that rules are off" 400
expect "in plain words" "$(jq_ "'aren' in d['error'] and 'expire' in d['error']")" True

# --- rules on, with passwords that last 13 days ---
unlock
req adm POST /api/admin/overlays/apply "{\"overlay\":\"ppolicy\",\"action\":\"enable\",\"values\":{\"minLength\":\"8\",\"history\":\"0\",\"maxAgeDays\":\"13\",\"lockout\":\"false\",\"policyDN\":\"$PDN\"},\"confirmed\":true}"; check "password rules turned on (13 days)" 200
req adm POST /api/admin/users "{\"username\":\"$U\",\"attrs\":{\"givenName\":[\"Ex\"],\"sn\":[\"Pire\"],\"mail\":[\"$U@example.org\"]},\"password\":\"$P1\"}" >/dev/null
req adm POST /api/admin/users "{\"username\":\"$N\",\"attrs\":{\"givenName\":[\"No\"],\"sn\":[\"Mail\"]},\"password\":\"$P1\"}" >/dev/null
b=$(count)
req adm POST /api/admin/mail/reminders; check "Check now" 200
expect "the person with an address is reminded" "$(jq_ "d['sent']")" 1
waitmail $((b+1)); expect "the email says 13 days" "$(last | grep -c '^Subject: Your Dapper password expires in 13 days')" 1
expect "it went to them, greeted by first name" "$(last | grep -c "^To: <$U@example.org>")$(last | grep -c '^Hello Ex,')" 11
expect "and tells them how to change it" "$(last | grep -c 'Change password')" 1
req adm POST /api/admin/mail/reminders; expect "running it again sends nothing more" "$(jq_ "d['sent']")" 0
req adm GET /api/admin/mail; expect "the page shows the last check" "$(jq_ "'lastRun' in d['reminders']")" True

# --- later reminder points: the password now lasts 6 days, then 1 ---
maxage 518400
req adm POST /api/admin/mail/reminders; expect "with 6 days left the next reminder goes out" "$(jq_ "d['sent']")" 1
waitmail $((b+2)); expect "it says 6 days" "$(last | grep -c '^Subject: Your Dapper password expires in 6 days')" 1
maxage 86400
req adm POST /api/admin/mail/reminders; expect "with 1 day left the last reminder goes out" "$(jq_ "d['sent']")" 1
waitmail $((b+3)); expect "it says tomorrow" "$(last | grep -c '^Subject: Your Dapper password expires tomorrow')" 1
maxage 1; sleep 3
req adm POST /api/admin/mail/reminders; expect "a password that has already expired gets no reminder" "$(jq_ "d['sent']")" 0

# --- locked people and the on/off switch ---
maxage 1123200
req adm POST "/api/admin/user/suspend?dn=$(pdn $U)"; check "a person is suspended" 200
b2=$(count)
req adm POST /api/admin/mail/reminders; expect "nobody suspended is reminded" "$(jq_ "d['sent']")" 0
sleep 1; expect "no email went out" "$(count)" "$b2"

req adm GET /api/admin/mail; expect "the switch is on" "$(jq_ "d['mail']['remindExpiry']")" True
req adm PUT /api/admin/mail "{\"enabled\":true,\"remindExpiry\":false,\"host\":\"127.0.0.1\",\"port\":$MAIL_PORT,\"security\":\"none\",\"from\":\"dapper@example.org\",\"publicURL\":\"$BASE\"}"
req adm GET /api/admin/mail; expect "and it can be turned off" "$(jq_ "d['mail']['remindExpiry']")" False

echo; echo "passed $PASS, failed $FAIL"; [ "$FAIL" = 0 ]
