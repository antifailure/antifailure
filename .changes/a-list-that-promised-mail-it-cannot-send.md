# fixed

The waitlist told people they would hear from us. Nothing could have done that.
`POST /api/waitlist` writes one row and sends nothing, the table has no read
path by design, and antifailure.dev has no mail exchanger and an SPF policy
that authorizes no outbound sender at all. So somebody who left an address and
waited was waiting for a message that had no route to them, and the only signal
they would ever get was silence.

The confirmation now says what is stored, who touches it, and what will not
happen: a person reads the list when there is a hosted environment to connect a
repository to, there is no date for that, and nothing mails anybody on a
schedule. It then names the two routes that resolve sooner, the quickstart,
which needs no account and works today, and booking a call, which is a real
calendar. The same correction is on the contact page and in the metadata for
the sign-up route, which carried the promise into every tab title and link
preview.

Two other claims on the site were describing things that are not there. The
privacy sheet said passwords entered in the form are not stored, and the form
has had no password field since the fake one was removed. The legal pages
listed what a waitlist row holds and omitted the page it was submitted from,
which is stored.
