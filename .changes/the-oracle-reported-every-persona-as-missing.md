# fixed

`af oracle` no longer reports a manifest's personas as rows the change stopped
writing.

Both sides of a comparison provision the same personas, each into its own
database, and each database gives the persona's rows its own generated key.
Rows were matched only by key, so on an identical build the owner's account
read as a row missing on one side and a row extra on the other, along with
every membership that pointed at it. Antifailure's own manifest reported six
major differences this way on a build that changed nothing.

A row the two sides do not share by key is now matched when it names a persona,
by the persona's address or phone number or by a UUID already matched that way.
The matched row is still compared column by column, so a persona provisioned
under a different name is reported as a changed row. A match is made only when
it is the only one on both sides; two rows for one persona on each side are
reported as before.

A changed row whose differing columns are named like a digest, with `hash`,
`salt`, `digest` or `mac` as a word of the name, and whose two values look like
random values of the same length now carries a hint naming the
`oracle.ignore.fields` entry that would quiet it. A password hashed under each
side's own salt, or an audit chain's hash over each side's clock, differs on
every build, and the report used to give no sign of that. The finding is still
reported; the hint only says what to write if the column is what it looks like.
