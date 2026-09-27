# fixed

The crash-recovery check no longer fails when PostgreSQL recovers between two
availability probes. The probe samples every 100 ms; a logged 69 to 70 ms crash
can leave every query answered. The check now preserves that observation
instead of requiring an unseen refusal, and compares the report to the
queries the probe actually measured rather than demanding exact agreement
with PostgreSQL's separate process-recovery timestamps.
Reports also carry the actual number of probe attempts, so an empty sampler
cannot be presented as a database that answered every query.
