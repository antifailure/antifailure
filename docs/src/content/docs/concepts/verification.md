---
title: Verification
description: Why a golden is scanned after masking, and why an unverified one cannot be branched.
sidebar:
  order: 3
---

Masking is a claim. Verification is a check.

After the masking rules run, the engine scans the candidate for data that still
looks like a person: addresses, card numbers, national identifiers, names in
free text. If it finds anything, nothing is published. If it finds nothing, it
signs a statement of what it scanned and what it found, and that statement is
what makes the version branchable.

```
copy ──> mask ──> scan ──> attestation ──> golden
                    │
                    └── anything found: nothing is published
```

## Why the check is separate from the rules

Because the rules are written by people. A column added last month has no rule,
a rule can name the wrong column, and a `notes` field can hold an address
somebody pasted into it. A masking pass that ran successfully proves the rules
ran, not that the data is safe.

Verification is the part that can say no.

## Nothing branches an unverified golden

```
AF-MSK-001 The golden gv_20260826120000_a1b2c3d4 has no valid verification
attestation and cannot be branched.
  Next: Run 'af golden verify gv_...'; a golden is branchable only once
  verification has passed.
```

This is enforced in code rather than in a checklist. It is the product's
central promise: an environment cannot contain unmasked production data,
because the only thing an environment can branch is a golden, and a golden is
not a golden until the scan passed.

**Where it is enforced differs by provider, and the difference is worth
knowing.** Neon, Supabase and Database Lab check the attestation at branch time
and refuse with `AF-MSK-001`. The Docker provider, which is the default on a
laptop, refuses earlier instead: a refresh whose verification fails never
commits an image, so there is no unverified golden in existence to branch. That
is the stronger place to refuse, and it is why the conformance behaviour named
below passes for it.

**It is not equivalent, and this page used to say it was.** Two things follow
from the Docker provider treating the existence of an image as the
verification, and a reader relying on this page should have both:

- A golden the provider lists is reported as verified because the image is
  there, not because anything re-read the attestation.
- Re-running `af golden verify` on a published golden and having it FAIL does
  not stop that golden being branched again, because nothing marks it
  unverified afterwards. On the other three providers the next branch is
  refused.

The conformance suite every provider runs has a behaviour for exactly this, so
a provider written outside this repository is held to it too.

## When the scan finds something

```
AF-MSK-002 Verification found data matching card number in orders.notes.
  Next: Add a masking rule for orders.notes and refresh the golden. The value
  itself is never printed.
```

The value is never printed, and it is never written to a log, an artifact, or a
CI annotation. A finding that quoted the data would publish it in the output of
the job that caught it.

Add a rule and refresh:

```yaml
# masking.yaml
rules:
  - table: orders
    column: notes
    transform: free_text
    why: "customers paste anything into this field"
```

If the column genuinely holds no personal data and the detector is wrong, say
so explicitly rather than deleting the check:

```yaml
  - table: orders
    column: notes
    transform: preserve
    why: "internal fulfilment codes, never free text from a customer"
```

`preserve` is the exemption, and `why` is what makes it reviewable. An
exemption with no sentence beside it is a decision nobody can check later, and
`af mask plan` prints the sentence next to the column so it is read.

## The attestation

A signed statement: which version, which rules, which detectors ran, how many
rows and columns were scanned, and what was found. It is stored with the golden
so anyone holding an environment can read what was checked without asking the
engine.

With the Neon provider it lives in the branch itself:

```sql
SELECT version, rules_hash, created_at, attestation FROM _antifailure.golden;
```

It is signed so that "this data was scanned" is a claim you can check rather
than one you have to take on trust.

Related: [masking](/docs/concepts/masking/), [goldens](/docs/concepts/goldens/).
