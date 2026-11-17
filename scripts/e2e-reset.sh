#!/usr/bin/env bash
# End-to-end tests for forgotten passwords: the emailed one-time link, the same answer for every name, the password
# rules, links that work once and die with the next request, sessions ended, and the "password changed" notices.
# Needs a configured Dapper plus the demo directory, and starts a tiny mail server on this machine that Dapper sends
# to (so Dapper must be able to reach 127.0.0.1:$MAIL_PORT, i.e. run on this machine, not in a container).
set -u
BASE=${BASE:-http://localhost:8080}
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
R=$RANDOM; U=rs$R; N=rn$R; P1="first-long-pass-$R"; P2="second-long-pass-$R"; P3="third-long-pass-$R"
pdn() { echo "uid=$1,ou=people,dc=example,dc=org"; }
cleanup() { req adm DELETE "/api/admin/user?dn=$(pdn $U)" >/dev/null; req adm DELETE "/api/admin/user?dn=$(pdn $N)" >/dev/null
  req adm PUT /api/admin/mail '{"enabled":false,"port":587,"security":"starttls"}' >/dev/null; kill $SINK 2>/dev/null; rm -rf "$J"; }
trap cleanup EXIT
count() { grep -c '^=====$' "$LOG"; }
waitmail() { for _ in $(seq 1 40); do [ "$(count)" -ge "$1" ] && return 0; sleep 0.25; done; return 1; }
last() { python3 - "$LOG" <<'PY'
import sys; t = open(sys.argv[1]).read().split("=====\n"); print([m for m in t if m.strip()][-1])
PY
}
tokenof() { last | grep -o '#/reset/[A-Za-z0-9_-]*' | head -1 | cut -d/ -f3; }

req adm POST /api/login '{"username":"alice","password":"alicepass"}'; check "admin sign-in" 200

# --- not offered until email is on ---
req adm PUT /api/admin/mail '{"enabled":false,"port":587,"security":"starttls"}'
req anon GET /api/password/forgot; expect "the sign-in page isn't offered a reset link while email is off" "$(jq_ "d['enabled']")" False
req anon POST /api/password/forgot "{\"username\":\"alice\"}"; check "asking while email is off is refused" 404

req adm PUT /api/admin/mail "{\"enabled\":true,\"host\":\"127.0.0.1\",\"port\":$MAIL_PORT,\"security\":\"none\",\"from\":\"dapper@example.org\",\"publicURL\":\"$BASE\"}"; check "email turned on" 200
req anon GET /api/password/forgot; expect "now a reset link is offered" "$(jq_ "d['enabled']")" True

# --- two people: one with an address, one without ---
req adm POST /api/admin/users "{\"username\":\"$U\",\"attrs\":{\"givenName\":[\"Re\"],\"sn\":[\"Set\"],\"mail\":[\"$U@example.org\"]},\"password\":\"$P1\"}" >/dev/null
req adm POST /api/admin/users "{\"username\":\"$N\",\"attrs\":{\"givenName\":[\"No\"],\"sn\":[\"Mail\"]},\"password\":\"$P1\"}" >/dev/null
req old POST /api/login "{\"username\":\"$U\",\"password\":\"$P1\"}"; check "the person signs in (this session should end at the reset)" 200

# --- the answer is the same for everyone ---
req anon POST /api/password/forgot "{\"username\":\"$U\"}"; check "asking for a link" 200; A=$BODY
req anon POST /api/password/forgot "{\"username\":\"nobody-$R\"}"; check "asking for a name that doesn't exist" 200
expect "the answer is identical for a real and an unknown name" "$BODY" "$A"
req anon POST /api/password/forgot "{\"username\":\"$N\"}"; expect "and for a person with no email address" "$BODY" "$A"
waitmail 1; expect "exactly one email was sent (nothing for the unknown name or the person with no address)" "$(count)" 1
sleep 1; expect "still one" "$(count)" 1
expect "it went to the address on file" "$(last | grep -c "^To: <$U@example.org>")" 1
TOK=$(tokenof); expect "it holds a link to Dapper" "$([ -n "$TOK" ] && echo yes)" yes
expect "the link starts with Dapper's own address" "$(last | grep -c "$BASE/#/reset/")" 1

# --- the link ---
req anon POST /api/password/reset/check "{\"token\":\"$TOK\"}"; check "the link checks out" 200
req anon POST /api/password/reset/check '{"token":"made-up"}'; check "a made-up link is refused" 400
expect "and explained" "$(jq_ "'expired' in d['error']")" True
req anon POST /api/password/reset "{\"token\":\"$TOK\",\"password\":\"short\"}"; check "a password that breaks the rules is refused" 400
expect "with the rule named" "$(jq_ "'at least 8' in d['error']")" True
req anon POST /api/password/reset/check "{\"token\":\"$TOK\"}"; check "and the link still works afterwards" 200
req anon POST /api/password/reset "{\"token\":\"$TOK\",\"password\":\"$P2\"}"; check "a good password is accepted" 200
req anon POST /api/password/reset "{\"token\":\"$TOK\",\"password\":\"$P3\"}"; check "the link can't be used a second time" 400
req new POST /api/login "{\"username\":\"$U\",\"password\":\"$P2\"}"; check "the new password signs in" 200
req bad POST /api/login "{\"username\":\"$U\",\"password\":\"$P1\"}"; check "the old password no longer does" 401
req old GET /api/me; check "the session that was open before the reset is signed out" 401
waitmail 2; expect "a 'password changed' notice was sent" "$(last | grep -c '^Subject: Your password was changed')" 1
expect "and it never contains the password" "$(last | grep -c "$P2")" 0

# --- a newer request replaces the older link ---
req anon POST /api/password/forgot "{\"username\":\"$U\"}"; waitmail 3; T1=$(tokenof)
req anon POST /api/password/forgot "{\"username\":\"$U\"}"; waitmail 4; T2=$(tokenof)
expect "the second link is different" "$([ "$T1" != "$T2" ] && echo yes)" yes
req anon POST /api/password/reset/check "{\"token\":\"$T1\"}"; check "the first link no longer works" 400
req anon POST /api/password/reset/check "{\"token\":\"$T2\"}"; check "the newest one does" 200

# --- other ways a password changes send a notice, and end an open link ---
req new POST /api/me/password "{\"current\":\"$P2\",\"new\":\"$P3\"}"; check "a person changes their own password" 200
waitmail 5; expect "that sends a notice too" "$(last | grep -c '^Subject: Your password was changed')" 1
req anon POST /api/password/reset/check "{\"token\":\"$T2\"}"; check "and an open link is cancelled by it" 400
req adm POST "/api/admin/user/password?dn=$(pdn $U)" '{}'; check "an administrator resets a password" 200
waitmail 6; expect "that sends a notice as well" "$(last | grep -c 'An administrator reset it')" 1

# --- suspended people get nothing ---
req adm POST "/api/admin/user/suspend?dn=$(pdn $U)"; check "a person is suspended" 200
req anon POST /api/password/forgot "{\"username\":\"$U\"}"; check "asking for a link for them" 200
sleep 1.5; expect "no email is sent for a suspended account" "$(count)" 6

# --- activity record ---
req adm GET "/api/admin/audit?q=$U&category=password&page=1&pageSize=50"
expect "requests and the reset are in the activity record" "$(jq_ "any(e['action']=='password.reset' for e in d['events'])")" True

echo; echo "passed $PASS, failed $FAIL"; [ "$FAIL" = 0 ]
