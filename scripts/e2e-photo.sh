#!/usr/bin/env bash
# End-to-end tests for profile photos. Needs a configured Dapper plus the demo directory.
set -u
BASE=${BASE:-http://localhost:8080}
PASS=0; FAIL=0
J=$(mktemp -d)
JPG='/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/2wBDAQkJCQwLDBgNDRgyIRwhMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjL/wAARCAAQABADASIAAhEBAxEB/8QAHwAAAQUBAQEBAQEAAAAAAAAAAAECAwQFBgcICQoL/8QAtRAAAgEDAwIEAwUFBAQAAAF9AQIDAAQRBRIhMUEGE1FhByJxFDKBkaEII0KxwRVS0fAkM2JyggkKFhcYGRolJicoKSo0NTY3ODk6Q0RFRkdISUpTVFVWV1hZWmNkZWZnaGlqc3R1dnd4eXqDhIWGh4iJipKTlJWWl5iZmqKjpKWmp6ipqrKztLW2t7i5usLDxMXGx8jJytLT1NXW19jZ2uHi4+Tl5ufo6erx8vP09fb3+Pn6/8QAHwEAAwEBAQEBAQEBAQAAAAAAAAECAwQFBgcICQoL/8QAtREAAgECBAQDBAcFBAQAAQJ3AAECAxEEBSExBhJBUQdhcRMiMoEIFEKRobHBCSMzUvAVYnLRChYkNOEl8RcYGRomJygpKjU2Nzg5OkNERUZHSElKU1RVVldYWVpjZGVmZ2hpanN0dXZ3eHl6goOEhYaHiImKkpOUlZaXmJmaoqOkpaanqKmqsrO0tba3uLm6wsPExcbHyMnK0tPU1dbX2Nna4uPk5ebn6Onq8vP09fb3+Pn6/9oADAMBAAIRAxEAPwCrRRRXvngn/9k='
echo "$JPG" | base64 -d > "$J/ok.jpg"
printf 'GIF89a\x01\x00\x01\x00' > "$J/fake.jpg"                     # not a JPEG
printf '\xff\xd8\xff\xe0garbage-after-the-header' > "$J/broken.jpg"   # starts like a JPEG but isn't one
head -c 3000000 /dev/zero > "$J/big.jpg"; printf '\xff\xd8\xff' | dd of="$J/big.jpg" conv=notrunc 2>/dev/null
req() { local jar=$1 m=$2 p=$3 f=${4:-}
  local out; out=$(curl -s -w '\n%{http_code}' -b "$J/$jar" -c "$J/$jar" -X "$m" -H 'X-Requested-With: dapper' ${f:+-H 'Content-Type: image/jpeg' --data-binary "@$f"} "$BASE$p" | tr -d '\0')
  CODE=${out##*$'\n'}; BODY=${out%$'\n'*}; }
jreq() { local out; out=$(curl -s -w '\n%{http_code}' -b "$J/$1" -c "$J/$1" -X "$2" -H 'X-Requested-With: dapper' -H 'Content-Type: application/json' ${4:+-d "$4"} "$BASE$3"); CODE=${out##*$'\n'}; BODY=${out%$'\n'*}; }
jq_() { python3 -c "import sys,json;d=json.load(sys.stdin);print(eval(sys.argv[1]))" "$1" <<<"$BODY"; }
check()  { if [ "$CODE" = "$2" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got $CODE, want $2): ${BODY:0:200}"; fi; }
expect() { if [ "$2" = "$3" ]; then PASS=$((PASS+1)); echo "ok   $1"; else FAIL=$((FAIL+1)); echo "FAIL $1 (got '$2', want '$3')"; fi; }
BOB="uid=bob,ou=people,dc=example,dc=org"; ALICE="uid=alice,ou=people,dc=example,dc=org"
q() { python3 -c "import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))" "$1"; }
cleanup() { req adm DELETE "/api/admin/user/photo?dn=$(q $BOB)" >/dev/null; req bob DELETE /api/me/photo >/dev/null
  jreq adm PUT /api/admin/settings/fields '{"fields":[{"attr":"jpegPhoto","userVisible":true,"userEditable":true}]}' >/dev/null; rm -rf "$J"; }
trap cleanup EXIT

jreq adm POST /api/login '{"username":"alice","password":"alicepass"}'; check "admin sign-in" 200
jreq bob POST /api/login '{"username":"bob","password":"bobpass"}'; check "user sign-in" 200
req bob GET /api/photo; check "no picture yet" 404

req bob PUT /api/me/photo "$J/ok.jpg"; check "set your own picture" 200
req bob GET /api/photo; check "read it back" 200
curl -s -b "$J/bob" "$BASE/api/photo" -o "$J/back.jpg" -D "$J/hdr"
expect "it is served as a JPEG" "$(grep -ci '^content-type: image/jpeg' $J/hdr)" 1
expect "with the bytes of a JPEG" "$(head -c3 $J/back.jpg | xxd -p)" ffd8ff
jreq bob GET /api/me/profile
expect "profile says there is a picture" "$(jq_ "d['hasPhoto']")" True
expect "and the picture is not a text field" "$(jq_ "'jpegPhoto' in [f['attr'] for f in d['fields']]")" False
jreq adm GET "/api/admin/user?dn=$(q $BOB)"
expect "admins see it on the person" "$(jq_ "d['hasPhoto']")" True

req bob PUT /api/me/photo "$J/fake.jpg"; check "a GIF is refused" 400
req bob PUT /api/me/photo "$J/broken.jpg"; check "a damaged JPEG is refused" 400
req bob PUT /api/me/photo "$J/big.jpg"; check "an oversized file is refused" 413
req bob GET "/api/photo?dn=$(q $ALICE)"; check "you can't read someone else's picture" 403
req bob PUT "/api/admin/user/photo?dn=$(q $ALICE)" "$J/ok.jpg"; check "you can't set someone else's picture" 403

# an admin sets and clears pictures
req adm PUT "/api/admin/user/photo?dn=$(q $ALICE)" "$J/ok.jpg"; check "admin sets a picture for someone" 200
req adm GET "/api/photo?dn=$(q $ALICE)"; check "admin reads it" 200

# the People list shows who has a picture, and offers a small copy
jreq adm GET "/api/admin/users?q=alice"; expect "the list says alice has a picture" "$(jq_ "[u['hasPhoto'] for u in d['users'] if u['username']=='alice'][0]")" True
jreq adm GET "/api/admin/users?q=ada.reyes0"; expect "and that someone without one has none" "$(jq_ "[u.get('hasPhoto', False) for u in d['users'] if u['username']=='ada.reyes0'][0]")" False
curl -s -D "$J/th.h" -o "$J/th.jpg" -b "$J/adm" -H 'X-Requested-With: dapper' "$BASE/api/photo?dn=$(q $ALICE)&size=thumb"
expect "a small copy is served as a JPEG" "$(grep -ci '^content-type: image/jpeg' "$J/th.h")" 1
ETAG=$(grep -i '^etag:' "$J/th.h" | tr -d '\r' | cut -d' ' -f2)
expect "with a tag, so unchanged pictures aren't sent again" "$([ -n "$ETAG" ] && echo yes)" yes
expect "asking again with the tag gets 304" "$(curl -s -o /dev/null -w '%{http_code}' -b "$J/adm" -H 'X-Requested-With: dapper' -H "If-None-Match: $ETAG" "$BASE/api/photo?dn=$(q $ALICE)&size=thumb")" 304
req bob GET "/api/photo?dn=$(q $ALICE)&size=thumb"; check "the small copy follows the same rules (403 for others)" 403
req adm DELETE "/api/admin/user/photo?dn=$(q $ALICE)"; check "admin removes it" 200
req adm GET "/api/photo?dn=$(q $ALICE)"; check "it's gone" 404
jreq adm GET "/api/admin/users?q=alice"; expect "the list no longer shows a picture" "$(jq_ "[u.get('hasPhoto', False) for u in d['users'] if u['username']=='alice'][0]")" False
req adm DELETE "/api/admin/user/photo?dn=$(q $ALICE)"; check "removing again is harmless" 200

# the administrators can stop people changing their own picture
jreq adm PUT /api/admin/settings/fields '{"fields":[{"attr":"jpegPhoto","userVisible":true,"userEditable":false}]}'; check "turn off self-service pictures" 200
req bob PUT /api/me/photo "$J/ok.jpg"; check "people can no longer change their own" 403
req bob DELETE /api/me/photo; check "or remove it" 403
req bob GET /api/photo; check "but can still see theirs" 200
jreq adm PUT /api/admin/settings/fields '{"fields":[{"attr":"jpegPhoto","userVisible":true,"userEditable":true}]}'
req bob DELETE /api/me/photo; check "remove your own picture" 200
req bob GET /api/photo; check "and it's gone" 404

echo; echo "$PASS passed, $FAIL failed"; [ "$FAIL" = 0 ]
