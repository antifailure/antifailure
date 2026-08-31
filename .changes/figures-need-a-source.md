# added

`just figurecheck` refuses a number on the marketing site that reads as a
measurement, a percentage or an "N of M", unless `tools/docs/figure-exemptions.tsv`
says where it came from. A reason is required and an entry that stops being
needed fails the gate, so the list cannot rot into permissions nobody remembers
granting.

It reads source rather than built HTML, because the defect it was written for
was drawn client side: the site rendered an invented `fid 87%` fidelity score on
two product pages, and `curl` on either one found no "87" anywhere, so every
audit over the rendered output came back clean.
