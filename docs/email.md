# Email

Dapper can send email through a mail server you already have (your own, or a service such as your provider's SMTP
relay). It sends mail only for things people ask for or need to know, starting with password reset links.

Set it up under **Email** in the top bar (administrators only).

| Setting | What to enter |
|---|---|
| Mail server and port | Your provider's SMTP address, for example `smtp.example.org` and `587`. |
| Connection | **STARTTLS** (port 587) or **TLS from the start** (port 465). *No encryption* is accepted only for a mail server on the same machine. |
| Username and password | Leave empty if the server needs none. The password is stored in Dapper's data volume and never shown again; leaving the box empty keeps it, unless you change the server, port or username. |
| Send from | The address the mail comes from. Some providers only allow addresses they host. |
| Dapper's web address | What people type in a browser, such as `https://dapper.example.org`. Links in emails are built from it, never from the incoming request. |

Use **Send test message** before turning email on. It tries what is on the page, even if unsaved, and says what is
wrong in plain words (can't reach the server, wrong password, certificate not trusted, address refused).

If your mail server uses a certificate your computer does not trust, fix the certificate; the "accept a certificate that
isn't trusted" box is there for emergencies only.

Changes are recorded on the Activity page (settings saved, test messages sent or failed). Passwords are never recorded.

## Forgotten passwords

Once email is on, the sign-in page shows **Forgot your password?**. The person enters their username or email address
and Dapper emails a link to the address stored in their directory entry. The link:

- works once and expires after an hour (a newer request replaces it, and restarting Dapper cancels outstanding links);
- opens a page where they choose a new password, which has to follow the password rules;
- ends every session that account had, anywhere.

Dapper answers the same way whether or not the name exists or has an email address, so the page can't be used to find
out who has an account. People without an email address, and suspended people, can't use it: an administrator resets
those passwords from the People page, as before. Requests are limited per account and per address.

After any password change (a reset by link, someone changing their own password, or an administrator resetting it)
the person gets a "Your password was changed" email, so a change they didn't make is noticed. It never contains the
password.
