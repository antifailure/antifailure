# fixed

`AF-MSK-010` refuses masking on a table stored with an access method that
implements neither the ctid a keyless rewrite addresses a row by nor the
UPDATE a keyed one runs. The refusal is right and is unchanged. The sentence
after it was not: it said to run `af mask plan`, and from the state the
refusal leaves you in there is no branch, so that command answers `AF-DB-014
No database branch exists` and exits 5. On the other path it was worse, since
`af mask plan` is itself one of the three places that raise `AF-MSK-010`, so
the remedy there was the command that had just run.

The Next line now carries the remedy the message body already carried, which
is the one that works from here: give the column a rule that preserves it, or
change the table so masking can address a row in it. It still names `af mask
plan`, and now says what that command needs before it can answer.
