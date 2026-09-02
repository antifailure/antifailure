# fixed

The runs list, the verdicts view, the goldens quota, the masking
attestation table and the compliance pack's masking control were blank for
every real customer.

All five read `golden_versions`, `runs` and `verdicts`, and the only INSERTs
into those three tables anywhere in the repository were the test harness and
the staging seeder. The seeder fills all of them, so every one of these looked
correct in development and had nothing to show in production. The compliance
pack was the worst of it: it reported that the check ran and found nothing to
show, for an organization that produced a signed attestation every night.

The engine emitted the events, the sink mapped them and ingest accepted the
types, so nothing anywhere reported a problem; the projections were simply
never written, because the earlier fix for the same defect on environments was
written as a case for `environment.*` rather than as a rule about projections.

`golden.published` could never have been recorded even had it been projected,
because the event carried a phase, a version and a boolean while
`golden_versions` is keyed through the repository. It now carries the identity
every environment event carries, plus the rules digest, the size, the
attestation and the golden's own creation time. `af golden refresh` and the
pinned golden path emitted no event at all, so the two commands that most need
a compliance record produced none.
