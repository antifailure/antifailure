# fixed

A refused sign-in now gets a page rather than a raw JSON body in the address
bar.

Pressing Get started on the website, then Continue with GitHub, then
authorising the OAuth application, ended at
`{"error":"This installation is not open for sign-ups..."}` rendered as plain
text, with no heading, no explanation and no link back to the waitlist the
visitor had been standing on. It is the most prominent button on the site and
it ended in punctuation.

Three things changed.

An installation whose allowlist names nobody is now refused **before** the
browser is sent to GitHub. That answer never depended on who was asking, so
asking somebody to authorise an application in order to hear it was asking for
something in exchange for nothing. An allowlist that names some accounts still
has to be decided at the callback, because the list is keyed on the GitHub
login and nothing before the redirect carries it. What happens there instead is
that the authorization the refused person just granted is withdrawn again, so
being turned away no longer leaves a third party application on their GitHub
account.

Every address a person opens directly now answers a browser with a page and
every script with the JSON body it already got: sign-in, the GitHub and email
callbacks, the deleted-organization export link, and the rate limiter's answer
on all of them. The page names what happened in a sentence and, on a
deployment that sets `AF_SIGNUP_URL`, offers the one link that is worth
offering.

The refusal is now `403` rather than `400`. The request was understood, and
there was nothing about it the caller could have changed.
