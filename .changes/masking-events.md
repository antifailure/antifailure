# fixed

A golden refresh now says what it is doing on the event stream. `mask.planned`,
`mask.progress`, `mask.applied`, `mask.verifying`, `mask.verified` and
`mask.finding` were in the event catalog and on the generated reference page,
and nothing ever emitted one: masking reported its work to the terminal only,
and dashboard mode silences the terminal, so the longest part of a refresh drew
an empty pane that said unverified. A failed verification now puts every
finding on the stream at error level rather than only the first one the refusal
names.
