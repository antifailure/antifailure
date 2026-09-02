# fixed

Four published claims did not match the code, and all four were true when they
were written.

The terms page said "Sign-in is for the waitlist. There is no public production
control plane yet", and the privacy notice said "Sign-in today is for the
waitlist". Installing the GitHub App creates an organization: the installation
webhook inserts one and consults no allowlist, because an installation is the
moment a tenant begins and there is no earlier point at which to ask somebody
to sign up. The row lands on the plan the schema gives a new one.

Both pages now say what happens instead, with the limit that makes it accurate:
nothing can be run in that organization until somebody signs in, because
creating an environment requires an actor. So an organization can exist for an
account nobody admitted, and it can do nothing.

`legal-facts` gained three assertions keyed on the mechanism rather than on the
sentence, so the combination that was published fails: a page denying a public
control plane while the webhook still creates organizations. The two guards the
corrected wording leans on are held as well, because the sentence is only true
while creating an environment requires an actor.

# fixed

The CI step named "The rules classify every column" claimed `af mask plan`
refuses a column no rule names. It does not, and never did: the command exits 0
for an unclassified column of any type, because every refusal keys on problems
and a column with no transform cannot become one. The step is worth running and
the comment now says what it really catches, which is a plan that cannot be
built or cannot be run. Corrected in both workflows that carried it.
