#!/bin/sh
# Healthy when slapd answers on its local socket.
exec ldapsearch -LLL -Q -Y EXTERNAL -H "ldapi://%2Frun%2Fslapd%2Fldapi" -b cn=config -s base dn >/dev/null 2>&1
