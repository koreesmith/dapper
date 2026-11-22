#!/usr/bin/env bash
# The release version lives in one place: the VERSION file. This checks it is well formed, that the changelog has an
# entry for it at the top, and that nothing else carries a copy that could go stale. Run by CI.
set -u
cd "$(dirname "$0")/.."
FAIL=0
bad() { echo "FAIL $1"; FAIL=1; }
V=$(tr -d '[:space:]' < VERSION)
[[ "$V" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] && echo "ok   VERSION is $V" || bad "VERSION must look like 2.1.0 (got '$V')"
top=$(grep -m1 '^## ' CHANGELOG.md | sed 's/^## //')
[[ "$top" == "$V" || "$top" == "$V "* ]] && echo "ok   the newest changelog entry is for $V" || bad "the newest changelog entry is '$top', not $V"
if grep -qE '"version"' web/package.json; then bad "web/package.json has its own version; remove it, the VERSION file is the only place"; else echo "ok   package.json carries no version"; fi
if grep -nE 'DAPPER_VERSION|VERSION: *\$' docker-compose*.yml .env.example >/dev/null 2>&1; then bad "a compose file or .env.example sets the version again"; else echo "ok   compose files and .env.example don't set a version"; fi
[ "$FAIL" = 0 ]
