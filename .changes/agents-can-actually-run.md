# fixed

The headline capability had never once run end to end, and three separate
defects each stopped it on their own.

A persona whose `login` is `none` never signs in, so it has no account to
create, but persona provisioning insisted on somewhere to write one and refused
an application that has no users table. `examples/go-api` is exactly that
shape, a JSON API with a schema of customers and orders, so its one workflow
had never executed. A missing users table is fatal now only when some persona
actually needs an account.

A magic link and a one time code are both single use, and the runner read the
inbox with no floor. `waitFor` looks at what already arrived before it waits,
which is right for a message that need only exist and wrong for one that has to
be new, so a second attempt matched the first attempt's message and followed a
token the application had already spent. The inbox is watermarked before the
button is pressed now. It was a race rather than a certainty, which is how it
survived: driving this repository's own six workflows produced two that signed
in and four that did not, out of one code path in one run.

An application that renders on the client has nothing on it when the document
finishes parsing, and the runner snapshotted it there. This repository's own
console shows the single word "Loading" for about a second and a half after the
sign-in callback lands, so two workflows signed in successfully and were then
reported as having proved nothing, on a page a second away from showing
everything they were asked to look for. Every read of a page now waits for it
to stop fetching first.

`examples/go-api`, `examples/django-api` and `examples/next-app` carry
workflows a browser can actually drive, with expectations that name words the
page actually shows. The two Next and Django examples declared no workflows at
all, and go-api asked an agent to place an order through a page that does not
exist.
