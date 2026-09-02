# security

The trust boundary page now says that masking and the scan that checks it are
one instrument, not two, and what that means for the data a check comment can
carry.

Both decide what to look at from `information_schema.columns.data_type` and both
accept the same six values. A column outside that list is not emptied by the
fail-closed default, is not read by the verification scan, and is not listed
among the unclassified columns the plan asks about, because the flag that lists
it is set inside the branch that has already passed the type test. It is copied,
and nothing says so. `citext`, which is the ordinary Postgres type for an email
address or a username, reports as `USER-DEFINED` and is outside the list.

That compounds with the one path on which records already crossed the boundary.
An invariant that does not hold carries up to five rows into the check comment,
and a column the masking default could not see is copied into the branch
unchanged, so such a row can carry a real production value.

The page said the scan covers every column. It does not, and the sentence is
corrected rather than softened. It also no longer describes the scan as
verifying the masking, because a check that shares its subject's blind spot
verifies nothing about that blind spot.

Nothing in the masking behaviour changes here. Widening the list is not additive:
a column copied today would start being emptied, which changes `rules_digest`
and invalidates every existing golden, so it is a decision with a migration
attached rather than a patch. The page says that too.
