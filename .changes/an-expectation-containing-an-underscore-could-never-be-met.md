# fixed

An expectation containing an underscore could never be met.

A workflow expectation that is not quoted is judged by how many of its
meaningful words appear on the page. Pulling those words out stripped every
character that was not a letter or a digit, from anywhere in the word, so
`total_cents` was looked for on the page as `totalcents`. The page is searched
for the word as written, and no page shows `totalcents`, so the word scored zero
for the life of the manifest. With nothing hit and no failure banner to read,
the answer was `unclear`, which is reported as UNVERIFIED and exits zero: the
expectation could not pass and could not fail, whatever the application did, and
nothing anywhere said so. `order_id`, `user-name`, `v1.2.3` and
`application/json` were all unmatchable the same way, and a sentence carrying
one of them could clear the two thirds bar on its other words alone, so an
expectation could also PASS on a page that did not contain the identifier it
named.

This repository was itself an instance. The dogfood workflow
`a-visitor-finds-the-operator-door` expects "Operator sign-in" against the
operator portal, whose title is `Operator sign-in`, and `sign-in` became
`signin`, so half its expectation could never hit.

A word is now narrowed rather than rewritten: the punctuation around it is
trimmed, which is what the stripping was for, and the characters inside it are
kept. `Welcome back!` still matches a page saying `Welcome back`, and
`total_cents` now matches the page that shows it. The quoted form, which is what
anyone who hit this will have used as a workaround, is untouched and still
requires its string character for character.

An expectation left with no word to look for at all, because every word in it is
a connective or shorter than three letters, is now named in the report. Silent
unmatchability was the defect; the wrong verdict was only its symptom, and the
advice that used to be printed instead sent the reader to look at a page that
may have been showing exactly what was asked for.
