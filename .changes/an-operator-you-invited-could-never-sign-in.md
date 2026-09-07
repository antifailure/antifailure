# added

An operator invited through the portal could never sign in.

Creating one writes the row with no password, on purpose, and told the reader
to "set a password out of band before it is usable". There was no out of band
that a person in the portal could reach: the only thing in this repository that
had ever written `admin_users.password_hash` was the `set-operator-password`
command, which runs on a connection string row level security does not apply
to. So every account created from that panel sat in the directory reading
`Not provisioned` and `Never`, and finishing the invitation needed a shell and
the admin database URL.

`admin.operators.setPassword` is the other half, under an operator session and
recorded in the platform audit chain at critical severity. The console asks for
the password in the panel that created the account and in the account's own
drawer, offers to generate one in the browser, and shows it once with the
sentence that says nothing on the platform can read it back. The server never
chooses a password and still mints none.

Setting your own password needs your current one as well, which nothing else on
this router asks for. The danger there is the opposite of the one setRole and
suspend guard: an operator cookie lasts twelve hours, and without the check a
stolen one buys the account permanently, because the thief sets a password and
the revoke below cuts every session the real owner had.

Every live session belonging to that operator is revoked in the same
transaction, except the one making the request. A password changed because it
leaked, that leaves the sessions it opened alive, is a reset that resets
nothing.

The length floor and the refusal of a stray newline now live in one module that
both writers ask, because a rule enforced in one of two writers is a rule with a
way around it, and the way around it would have been the one reachable from a
browser.
