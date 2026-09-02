# fixed

The Deploy workflow cancelled its own publish part way through.

Every workflow in this repository sets `cancel-in-progress: true`, which is
right for the ones answering "is this commit good": a superseded answer is
worth nothing. Deploy is not answering a question. It uploads to production,
and the steps after the upload are the ones that check the upload worked.

Run 33598095533, head 8fd7fedf, is what this looks like. "Assemble one site"
and "Check the assembled site before publishing it" both succeeded, "Publish"
was cancelled mid flight by the next merge, and the four steps behind it were
skipped. A partial upload reached the live site and nothing checked the
result. "The API answers" and "The machine-readable surfaces answer, and
describe this revision" exist precisely because a publish is not a working
endpoint, and cancellation takes them away first.

The step named "Say so when it was not published" is skipped too, so the
failure is silent by construction: the alarm sits inside the blast radius.
Eleven consecutive Deploy runs were cancelled this way and the last one to
report success was three hours and eight merges earlier.

Deploy now queues rather than cancels. That costs a few minutes of latency
when merges land back to back, which is the correct trade against a half
uploaded site nobody was told about.
