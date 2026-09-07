# The share of production a twin holds, per table

Measured 2026-09-07 by
`go test ./internal/fidelity -run TestBenchmarkTheShareOfProductionInTheTwin`,
from engine/internal/fidelity/volume_score_test.go, on the manifest in
engine/internal/fidelity/testdata/product-analytics.yaml against the volume
profile in engine/internal/fidelity/testdata/production-volume.json.

The profile is the committed record of what production holds, collected by
`af volume record` over a read only connection. It reads no row: every
figure comes from pg_class, pg_stats and the partition catalogs. Run it
against your own database and the table below is yours.

Collected 2026-09-01.

| Table | This twin | Production | Share |
| --- | --- | --- | --- |
| public.events | 120,000 | 4,200,000,000 | 0.0028 percent |
| public.event_props | 40,000 | 1,800,000,000 | 0.0022 percent |
| public.sessions | 12,000 | 310,000,000 | 0.0038 percent |
| public.audit_log | 50 | 126,000,000 | 0.000039 percent |
| public.users | 6,200 | 8,400,000 | 0.073 percent |
| public.memberships | 2,400 | 410,000 | 0.58 percent |
| public.insights | 420 | 240,000 | 0.17 percent |
| public.orgs | 1,900 | 140,000 | 1.3 percent |
| public.dashboards | 700 | 92,000 | 0.76 percent |
| public.api_keys | 95 | 38,000 | 0.25 percent |
| public.experiments | 140 | 3,400 | 4.1 percent |
| public.feature_flags | 95 | 1,200 | 7.9 percent |
| **all twelve** | **184,000** | **6,445,324,600** | **0.0028 percent** |

What the fidelity report says about that database, on the same
environment, three ways:

| Instrument | The database's data component | Score |
| --- | --- | --- |
| At d02fc3de, before this lane | `reproduced`, 12 tables over 184000 rows | 9 of 10, 90 percent |
| This build, no volume profile | `unmeasured`, and it says nothing compared it | **8 of 9, 89 percent** |
| This build, with the profile | `substituted`, with the fraction | **8 of 10, 80 percent** |

The sentence the last row prints, in full:

> 12 tables over 184,000 rows, branched from gv_20260830120000_abcd1234. Measured against the volume profile collected on 2026-09-01, 184,000 rows against production's 6,445,324,600 rows, which is 0.0028 percent. Its smallest table is public.audit_log, at 0.000039 percent of production's 126,000,000 rows. A timing measured against this branch is a lower bound and not a prediction

The middle row is the one worth arguing about. An environment nobody has
given a profile scores HIGHER than one that has, because an unmeasured
component leaves the denominator while a substituted one stays in it. That
is deliberate: an unknown is not a smaller pass, and the report names the
component and how to measure it rather than scoring a guess. The number
that goes down is the one on the row where somebody actually looked.

The lock timings a migration rehearsal prints against a branch this size
are lower bounds. `af insights` now says so, and states the extrapolation
to production's row counts as an extrapolation.
