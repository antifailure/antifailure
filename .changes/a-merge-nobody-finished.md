# fixed

The control plane's configuration reference shipped with an unresolved merge in
it. Literal `<<<<<<< HEAD`, `=======` and `>>>>>>>` around six rows of the
variable table, two of them the same variable described twice, on the one page
somebody reads to configure this product.

The resolution is the union of both sides rather than either of them, which is
why it was not obvious: each side held a row the other did not, and each held a
better version of one they shared. `AF_GITHUB_APP_INSTALL_URL` keeps the newer
description, because the console stopped hiding the membership recheck behind
the install address and the older text still described the screen before that
fix. `AF_SIGNUP_URL` takes the other side, because the version that survived
said a refused visitor would be pointed at "somebody else's waitlist" and the
waitlist was removed in this same release. `AF_SITE_ORIGIN` and
`AF_LEAD_NOTIFY_EMAIL` existed on one side only.

`tools/conflictcheck` is the gate, and what it is for is narrower than it looks.
Every gate this repository has was green about this. Markdown does not fail to
parse. `prosecheck` reads punctuation. `varcheck` and `config-docs.test.ts` ask
whether a variable is documented, and a row inside a conflict block reads as
documented to all three. The only check that did go red went red for a
consequence rather than the cause: `wirecheck` reported a variable as documented
with no supported deploy able to set it, which sent two people to look at
Terraform, when the cause was four lines of git output in a table.

What it cannot catch is written into the tool rather than left to be discovered.
A conflict resolved by keeping one side whole, when the correct resolution was
the union, leaves no marker at all and is invisible to this and to git. The
instrument for that one is diffing the resolution against both parents.
