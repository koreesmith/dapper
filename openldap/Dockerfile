# The Dapper directory server: OpenLDAP from Debian stable packages, set up so that Dapper's
# guided setup can take it over. See docs/dapper-server.md.
FROM debian:13-slim

ARG VERSION=dev
LABEL org.opencontainers.image.title="Dapper OpenLDAP server" \
      org.opencontainers.image.description="An OpenLDAP directory server prepared to be set up and managed by Dapper" \
      org.opencontainers.image.source="https://github.com/koreesmith/dapper" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}"

# slapd-contrib carries the lastbind overlay. Packages come from the pinned Debian release. The upgrade
# brings in security fixes published since the base image was built.
RUN apt-get update \
 && apt-get upgrade -y \
 && apt-get install -y --no-install-recommends slapd slapd-contrib ldap-utils openssl ca-certificates \
 && rm -rf /var/lib/apt/lists/* /etc/ldap/slapd.d/* /var/lib/ldap/* \
 # Group 10001 is Dapper's group: it lets Dapper read the first-start hand-off file (and nothing else).
 && groupadd -g 10001 dapper && usermod -aG dapper openldap \
 && mkdir -p /run/slapd /etc/ldap/tls /handoff /backups \
 && chown openldap:openldap /run/slapd /etc/ldap/tls /var/lib/ldap /etc/ldap/slapd.d \
 && chown openldap:dapper /handoff && chmod 2770 /handoff \
 && chown openldap:dapper /backups && chmod 2770 /backups

COPY schema/ /usr/share/dapper-ldap/schema/
COPY config.ldif.template /usr/share/dapper-ldap/config.ldif.template
COPY entrypoint.sh healthcheck.sh backup.sh backup-loop.sh restore.sh /usr/local/bin/
RUN chmod 0755 /usr/local/bin/*.sh
ENV DAPPER_LDAP_VERSION=${VERSION}

# slapd's configuration, the directory data, TLS files, the one-time hand-off to Dapper, and backups.
VOLUME ["/etc/ldap/slapd.d", "/var/lib/ldap", "/etc/ldap/tls", "/handoff", "/backups"]
EXPOSE 389 636
USER openldap
HEALTHCHECK --interval=15s --timeout=5s --start-period=20s --retries=5 CMD ["/usr/local/bin/healthcheck.sh"]
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
