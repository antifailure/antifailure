# fixed

`STATUS.md` carried two Phase 3 tables that disagreed. The sub-phase table said
the masking engine and the verification scanner were `partial`, with the rules
model, classifier, SQL compiler, resumable executor, streaming scan and signed
attestation all listed as still to come. A second table further down said all
of that was `proven`. Reading `internal/masking` and `internal/verify`
directly, and grepping their callers into `internal/env/golden.go`, confirms
the second table was right: the rules model, the SQL compiler and the
resumable executor exist and are wired, and so does the streaming scan and the
Ed25519 attestation. A previous reader trusted the stale table instead and
spent an hour concluding the product's headline feature was dead code.

The two tables are now one. The stale rows are corrected in place, the real
detail each table carried is kept rather than dropped, and the separate
per-package coverage table now says explicitly that its `State` column must
agree with the sub-phase table above it.
