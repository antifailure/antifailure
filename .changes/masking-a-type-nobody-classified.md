# fixed

The masking default and the verification scan that backstops it were keyed off
the same six element list of column types, so they were never two layers. A
column of any other type was not masked, was not listed among the columns no
rule covers, and was not read by the scan. It shipped into every preview
environment and the golden's attestation said it had been verified.

The types are not exotic. `information_schema` reports `citext` as
`USER-DEFINED` and `text[]` as `ARRAY`, checked against a real PostgreSQL 17,
and `citext` is what an application uses for an email column precisely because a
person types into it. A column called `email` was saved by the name based rule,
which ignores type. A column called `handle` on `citext` was not.

`looksSensitive` was a known-yes list with no known-no list beside it, so an
unrecognised type was silently treated as structural, as though somebody had
decided it was safe. There is now a `knownStructural` list of the types whose
text form cannot carry a sentence, and a type in neither list is reported as a
question. Nothing new is masked: masking a column that is copied today would
change what existing goldens hold and blank a column an environment may need,
which is a decision for whoever owns the product rather than a bug fix.

`af mask plan` also no longer tells you that every column it lists ships
unchanged. Some are emptied by the default and some genuinely ship, and a person
reading the list could not tell which of their columns had leaked.

# fixed

A verification scan that could not read a column reported itself clean. The
sentence "a column nobody could read is not a column that passed" was written
twice in `scan.go` and implemented nowhere, so `Clean()` counted findings and
ignored skips, and the golden was published on the strength of it. That was the
one way a golden could pass verification without having been verified.

Fixing it exposed a second defect immediately. The refusal path read
`report.Findings[0]` with no length check, which was safe only because every
path into it had a finding. A report whose only problem was an unreadable column
now reaches that line, so the fix for a silent pass would have shipped as a
panic on the golden refresh. Findings are reported first, and a skip now returns
`AF-MSK-011`, which says the column could not be read rather than claiming a
detector found something in it.
