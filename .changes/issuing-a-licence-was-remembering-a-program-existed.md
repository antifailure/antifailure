# fixed

Issuing a paid enterprise licence was somebody remembering that a Go program
exists, and the addresses a blocked customer was told to write to could not
receive mail.

`tools/licensegen` signs the keys `ee/engine/license` verifies. Searching the
tree for it found the tool, one test comment and a line in `.gitignore`. No
workflow ran it, no page described it, and nothing tied a payment to a key. A
command line tool run by hand is a legitimate design for something a vendor
does a handful of times a year with a key a pipeline must never hold. A command
nobody can find is not, so there is now a runbook: the key, the request, the
receipt, what the customer sets, how a reissue works, and what withdrawing one
actually amounts to.

Writing it against the verifier rather than against the generator turned up
three things the generator did not know.

The verifier's `Feature` type carried a comment saying a closed set means a
typo in an issued licence is caught when the licence is parsed. It is not.
`"features": ["ssoo"]` signed cleanly, parsed cleanly, reported the licence
active, and permitted nothing, with no error at either end and a customer
sitting on the community behaviour they had paid to leave. Watched happening
before it was changed. Parse is right to be permissive, because a licence
issued for a newer release names features an older binary has never heard of
and refusing the whole licence over one would cost the customer the features
they did buy. That leaves issue time as the only place the set can be closed,
so `licensegen issue` closes it, and the comment now says where and why. The
feature list is a copy, because the tools module is MIT and the package that
owns the list is not, and a test parses `license.go` and fails if the two ever
differ.

`seats` was an `int`, so a request that omitted it signed an unlimited licence
and said nothing. It is a pointer now and an absent field is refused, because
zero means unlimited to the verifier and that has to be written rather than
defaulted.

`-key-id` is a label the program cannot check against the key it signs with.
Get it wrong and the customer's engine looks the label up, finds a different
public key, and reports the licence as tampered with, which the licensing page
tells them almost always means a truncated paste. `issue` now prints the public
key belonging to the key that signed, on standard error so a pipe still carries
only the token, and the runbook says to compare it before sending anything.

Then the addresses. antifailure.dev publishes no MX record and its SPF policy
is `v=spf1 -all`, so every address on it is a promise nobody can keep. Eight
shipped. AF-EE-004 told a customer who had just hit their seat limit to email
licensing@, in the catalogue, in the generated Go, in `errors.v1.json`, on the
errors page, on the licensing page and in the control plane's seat refusal. The
enterprise licence text gave the same address for any question, and SECURITY.md
gave security@ to a researcher holding a finding, next to a paragraph about
nobody being on call for the mailbox. Each now names a route the contact page
already settled and that resolves today: GitHub private vulnerability
reporting, which is confirmed enabled on the repository, for a security
finding, and the contact page for anything commercial.

`tools/claimcheck` could not have caught any of it, for two reasons worth
separating. Five of the eight occurrences were outside every tree it read, and
two of the file types were outside its extension list. And every rule it has is
settled by a string in a repository file, while whether a domain accepts mail
is settled by DNS, which a hermetic build gate cannot ask. The contact page
changed that by stating it in the tree, which is exactly the anchor the premise
mechanism wants. So the rule exists now, over the error catalogue and the
enterprise licence code as well as the site, and it was watched failing on the
AF-EE-004 line and watched failing again when the premise was taken away.

Three things the runbook has to say plainly rather than describe. No release
builds the enterprise binary or stamps a public key into it, so a signed
licence verifies only where `AF_LICENSE_PUBLIC_KEYS` supplies the key. Nothing
records what was issued to whom. And `Verifier.Revoke` has no caller outside
its own test and no list is ever loaded, so the revoked state cannot be reached
by any shipped binary and withdrawing a licence means expiry, a key rotation
that invalidates every other licence on that key, or asking.
