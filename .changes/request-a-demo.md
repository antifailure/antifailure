# changed

Every "Start free" and "Create an account" button on the marketing site
pointed a stranger at a door that does not open. Self-serve organization
creation is gated by `AF_SELF_SERVE_SIGNUP`, which defaults off, so a GitHub
exchange from /signup created nothing: the header, the hero, the closing
panel, the pricing page, the solutions pages and the footer all promised an
account anybody could make, and the account never came. The page under them
described a free plan you land in by signing up, on a plane that admits no one
who signs up.

The hosted plane is reached by talking to a person now, and the site says so.
A new /request-demo page carries the split shell the sign-in screen uses: the
pitch and three claims the product actually makes on the left, and on the
right a request form a sales team reads. It asks for the Harvey field set,
name, business email, company, job title, an optional phone, an organization
type and a country, and a marketing opt-in that is unchecked by default. It
posts to the same POST /v1/leads the enterprise form does, so the request
lands in the product's own database and is announced over the same Resend
path, with a "demo" source so it is told apart from a contact-form lead. The
leads table has no column for a job title, an organization type or a country,
so those ride in the message as a labelled block a person reading the queue
can act on, which is what keeps this off a new table and its migration number.
Booking a call stays on /contact, where cal.com runs and reaches a person on a
known day; this page is the other half, for somebody not ready to pick a slot.

The form is built to the site's bar, not the happy path alone: it validates
every required field and the email format before the network and names the
first thing to fix, it keeps everything typed when a submission is refused, it
carries a hidden honeypot that answers a bot with the same confirmation and
writes nothing, and it renders idle, sending, one human error, and a
confirmation that replaces the form. There is no logo wall, because the site
carries none anywhere and a strip of borrowed marks is the one thing that
would make the page read as generated.

Every self-serve call to action leads there instead, and the copy beside each
one is corrected to match rather than left contradicting its own button.
/signup 301s to /request-demo, in public/staticwebapp.config.json and as a
MovedPage, so a bookmark or an indexed link still lands somewhere true, and it
leaves the route registry the way the moved product pages did. The auth
screen's sign-up variant is gone rather than left as a dead branch nothing
renders; signing in with GitHub stays for operators who already have an
organization, and the open-source quickstart stays the path that needs no
account at all.

Change 2 in the dispatch, wiring setOperatorPassword to a portal tRPC route,
is already on main: admin.operators.setPassword shipped in #349 with the
console form, the client wrapper and behavioural tests. It is not rebuilt here.
