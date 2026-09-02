# fixed

The CI job named "no credentials in the tree" ran the credential scanner
last, after eight document and claim checks. A job stops at its first
failing step, so a single em dash failing prosecheck reported that job as
FAILED under a name that makes a security claim, while `scanrepo` never
executed at all. Two reviewers read the red as a credential in the tree and
escalated, and neither could tell from the summary that the scan had not
happened, because a step that never ran looks exactly like a step that
failed.

The scan is now its own job, with that step as its only step, so green means
the scanner ran and found nothing rather than possibly meaning it never ran.
The remaining eight checks keep their own job under a name that describes
them, "the documents and the site tell the truth". A prose failure can no
longer prevent the credential scan from happening, which is the half of the
old arrangement that mattered.
