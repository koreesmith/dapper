# The service account

People sign in to Dapper with their own directory password, which Dapper verifies by
binding as them. Everything else goes through a **service account**: Dapper first decides
whether the signed-in person is allowed to do something (administrators group, the field
policy on the Settings page), then performs it as the service account. This is what lets
administrators manage everyone, and lets ordinary users edit their own details, without
granting every user write access in your directory's ACLs.

Because of that, treat the service account as high-value: give it a long random password,
supply it through the environment or a secret, and don't reuse it elsewhere.

## Access it needs

Under your users container (`users.baseDN`, and `users.suspendedBaseDN` if used):

- **read** the attributes Dapper shows (names, mail, phones, address fields, `title`,
  `departmentNumber`, `description`, `pwdAccountLockedTime`)
- **write** the same attributes, **add** and **delete** entries, **rename/move** entries
  (for suspend), and **write `userPassword`** (for password changes and resets)

Under your groups container (`groups.baseDN`): read and write the member attribute, and
add and delete groups.

## Example (OpenLDAP `olcAccess`) — adapt to your own policy

```
to attrs=userPassword
  by dn.exact="cn=dapper-svc,dc=example,dc=org" write
  by self write
  by anonymous auth
  by * none
to dn.subtree="ou=people,dc=example,dc=org"
  by dn.exact="cn=dapper-svc,dc=example,dc=org" write
  by self read
  by * read
to dn.subtree="ou=groups,dc=example,dc=org"
  by dn.exact="cn=dapper-svc,dc=example,dc=org" write
  by * read
```

If a change is refused for lack of permission, Dapper says so in plain language and
points at the service account.

The development stack in `docker-compose.yml` uses the directory's root account for convenience.
Don't do that in production.
