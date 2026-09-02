---
title: Compliance packs
description: SOC 2 and HIPAA evidence from what this installation recorded, and what the report deliberately does not say.
sidebar:
  order: 7
---

*Requires an enterprise license with the `compliance_packs` feature, and the
enterprise binary built from `ee/`.*

```sh
af compliance soc2 --org acme
af compliance hipaa --org acme --months 12 --output json --out evidence.json
```

## What this is not

It is not an audit report and it is not an opinion. It is a document that says
what this system recorded, names the artifact so somebody can go and look, and
leaves every conclusion to the person whose job that is.

A tool that printed "SOC 2 compliant" would be worse than no tool: it reads as
an opinion from somebody qualified to hold one and it is produced by a program
that looked at four tables.

## The four outcomes, three of which are not "pass"

**Evidenced.** The check ran, the artifact exists, and it says what the control
asks about. The artifact is named.

**Not evidenced.** The check ran and there was nothing to show. This is the
ordinary state of a new installation and it is not a failure. Most controls are
here on the first day.

**Failed.** The check found evidence that the control is *not* holding: an audit
chain with a break in it, a golden published without a clean scan, a membership
removal that did not revoke the member's sessions. This is the most important of
the four, because a compliance tool that cannot say no has no ability to say yes
that means anything.

**Outside this product.** The control is real and nothing here can speak to it:
physical security, background checks, a backup plan. Listed rather than quietly
omitted, because you need the whole framework and you need to know which parts
to go and get from somewhere else. A pack that showed only the controls it
happens to cover would read as a complete answer and would be about a third of
one.

Every control also says what *this product* covers of the requirement, which is
never all of it.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | A report was produced. |
| 6 | A control has evidence of not holding. |
| 3 | A configuration problem: no organisation, an unknown pack, no licence. |

Exit 6 is what a nightly job watches, so a broken audit chain stops a pipeline
without anybody having to parse the document. Controls that are merely not
evidenced do not fail the command: a command that failed on the first day would
be switched off within a week, taking the finding that matters with it.

## What it reads

**The audit log**, recomputed rather than believed. Each entry carries the hash
of the one before it, so altering or removing an entry leaves a break. Reading
the stored hash and comparing it to itself would pass on a rewritten log, which
is the only log where it matters, so every hash is recomputed from the entry's
contents.

Sequence gaps are reported and are not proof of tampering: the sequence comes
from a database sequence, and a rolled back transaction consumes a number
without writing a row. A deletion looks the same. The report says so rather than
choosing an interpretation.

**The masking attestations**, signature checked before anything they say is
repeated. An attestation is a signed statement that a golden was scanned and
found clean, and a report that repeated an altered one would launder it into
evidence.

**The privileges on the audit log**, read from the database rather than assumed
from a migration. The application role should hold `INSERT` and `SELECT` and
nothing else, so a rewrite is refused by the database rather than by a code path
somebody can forget to call. A grant of `UPDATE`, `DELETE` or `TRUNCATE` is
reported as failed.

**Row level security**, on every table holding tenant data, found by looking for
an `org_id` column in the catalogue rather than from a list in the source. A
table added next year and forgotten is exactly the table this has to notice. A
role holding `BYPASSRLS` is reported as failed, because it makes every policy
decorative.

**Environments and goldens**, for whether a copy of production-shaped data was
created from an unverified golden or left behind after teardown.

## The HIPAA de-identification control

`164.514(b)` is where this product does the most work and where its limits
matter most, so the pack says it plainly rather than in a footnote.

Every golden is scanned for real data before it can be branched, and the scan is
signed with what was looked at, how many rows were sampled, and the hash of the
rules used. **A scan is a sample and not a proof.** It is evidence that a masking
rule was applied and worked on what was read. It is not an expert determination
under `164.514(b)(1)`, and if you need one, this is an input to it rather than a
substitute for it.

## Configuration

```sh
export AF_CONTROL_PLANE_DATABASE_URL=postgres://reader@control-plane/antifailure
export AF_APP_ROLE=antifailure_app        # the role the APPLICATION connects as
export AF_AUDIT_RETENTION_DAYS=2555       # 0 means entries are never pruned
```

Run this as a role that can `SELECT` and nothing else. The whole document is a
read, and a tool that produces evidence about a database it can also write is a
tool whose evidence is worth less.

`AF_APP_ROLE` is the role the application connects as, whose privileges on the
audit log are one of the things reported on. It is not the role this command
connects as, and conflating the two would report on the wrong one.

Retention is read from configuration rather than from the database, because a
retention policy that has not yet deleted anything leaves no trace in the data.

## Partial reports

Evidence that could not be read is named at the top of the document and on the
control it belonged to:

```
> **This report is partial.** Some evidence could not be read, so the controls
> below rest on less than the full period:
>
> - masking-attestations: the golden versions could not be read: permission denied
```

A control reported as "not evidenced" because a query failed must not be
mistaken for one where there was genuinely nothing to show.
