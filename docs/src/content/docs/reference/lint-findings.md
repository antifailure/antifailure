---
title: Lint findings
description: Every finding the migration lint can report, and the identifier for each one that does not change between releases.
sidebar:
  order: 9
---

The migration lint reports what a migration will do to a table the size of
production. Each finding carries an identifier of the form `LINT-NNN`.

**The identifier is stable and everything else about a finding is not.** The
rule name, the title on this page, the sentence explaining what will happen and
the suggested fix are all prose, and they are rewritten whenever a clearer
wording exists. An identifier is assigned once and is never reused, including
after the rule that earned it is deleted, so something suppressing or counting
a finding should match on the identifier and nothing else.

This page is generated from `engine/internal/insights/lintcatalog.yaml`, so
it cannot fall behind the code: a rule with no entry there fails the build, an
entry naming no rule fails it too, and an identifier that goes missing after it
has been handed out fails it as well.

The machine readable form is at
[antifailure.dev/lint-findings.v1.json](https://antifailure.dev/lint-findings.v1.json).

[What each finding means and what to write instead](/docs/concepts/insights) is
on the insights page, beside the rest of what a rehearsal measures.

## Findings

| Identifier | Rule name | What it found |
| --- | --- | --- |
| `LINT-001` | `no_lock_timeout` | No lock_timeout, so a lock wait becomes an outage. |
| `LINT-002` | `not_null_without_default` | NOT NULL column added with no default. |
| `LINT-003` | `set_not_null_existing_column` | NOT NULL set on a column that already exists. |
| `LINT-004` | `alter_column_type` | Column type change that rewrites the table. |
| `LINT-005` | `index_not_concurrent` | Index built without CONCURRENTLY. |
| `LINT-006` | `drop_index_not_concurrent` | Index dropped without CONCURRENTLY. |
| `LINT-007` | `reindex_not_concurrent` | Index rebuilt without CONCURRENTLY. |
| `LINT-008` | `foreign_key_not_valid` | Foreign key added without NOT VALID. |
| `LINT-009` | `check_constraint_not_valid` | CHECK constraint added without NOT VALID. |
| `LINT-010` | `unique_constraint_builds_index` | Unique constraint that builds its index in place. |
| `LINT-011` | `backfill_in_ddl_transaction` | Rows changed in the same transaction as the schema. |
| `LINT-012` | `rename_column_in_use` | Column renamed while something still reads it. |
| `LINT-013` | `drop_column_in_view` | Column dropped while a view still selects it. |
| `LINT-014` | `vacuum_full` | VACUUM FULL, which rewrites the table offline. |
| `LINT-015` | `cluster` | CLUSTER, which rewrites the table offline. |
| `LINT-016` | `drop_table` | Table dropped. |
| `LINT-017` | `truncate` | Table truncated. |
