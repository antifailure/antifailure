# security

A sign-in link could send the person who used it to another site.

The address a sign-in returns you to was checked as a string: it had to begin
with one slash, not two, and carry no backslash. A URL parser deletes tabs and
line breaks before it reads anything, so `redirect_to=/%09/evil.test` passed
that check as `/<tab>/evil.test` and was then read by the callback as
`//evil.test`. GitHub sign-in and the emailed sign-in link both set the session
cookie and redirected to `https://evil.test/`, which put a phishing page at the
end of a genuine Antifailure sign-in.

A return target holding a control character is now refused, and what is left
must resolve to the application's own origin, so a signed-in person lands on
the dashboard instead.
