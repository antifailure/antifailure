# added

An on-call page (`docs/self-hosting/on-call`) covering the rotation, what an
acknowledgement means, when to wake somebody, and what to do first by class of
page, written for a team of one as much as a team of several.

The Azure page gains a full manual rollback procedure
(`docs/self-hosting/azure#upgrade-and-rollback-the-manual-path`) for the case
`deploy.sh`'s own automatic rollback does not fire: how to find the last good
revision, move traffic back to it, verify with `health-gate.sh`, and reason
about a migration that already applied, including the case where it was not
backward compatible and a code rollback alone would make things worse.
