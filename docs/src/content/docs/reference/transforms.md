---
title: Transform reference
description: Every masking transform, what it replaces a value with, and what it keeps.
sidebar:
  order: 8
---

Every transform available to a masking rule. This page is generated from
`engine/internal/masking/transform.go`, so a transform that exists and is not
here fails the build.

The **unique** column matters more than it looks. A transform that does not
preserve uniqueness cannot be used on a column with a unique constraint: the
masked values collide and the update fails partway. `af mask plan` catches that
before anything runs.

| Transform | Unique | What it does |
| --- | --- | --- |
| `address` | no | Replaces a street address with a synthetic one of a similar shape. |
| `city` | no | Replaces a city with a synthetic one. |
| `company` | no | Replaces a company name with a synthetic one that reads as a company. |
| `credit_card` | no | Replaces a card number with a Luhn valid test number, so a payment form still validates it and no real card is ever present. |
| `date_shift` | no | Moves a date or timestamp by a deterministic offset of up to a year, keeping its format and its time of day. |
| `email` | yes | Replaces an address with a unique synthetic one at example.test, which is reserved and can never receive mail. |
| `first_name` | no | Replaces a given name with a synthetic one. |
| `free_text` | no | Replaces prose with synthetic prose of a similar length, so a layout built for three paragraphs still gets three paragraphs. |
| `hash_hex` | yes | Replaces a value with a keyed hash of the same length. Equality is preserved and nothing else is. |
| `int_fpe` | no | Replaces an integer with a different one of the same digit count and sign, so range checks and column widths still hold. |
| `ip` | no | Replaces an IP address with one from a documentation range reserved by RFC 5737, which can never route anywhere. |
| `last_name` | no | Replaces a family name with a synthetic one. |
| `name` | no | Replaces a person's name with a synthetic one of a similar shape, keeping the number of parts. |
| `nullify` | no | Sets the column to null. This is the default for unclassified free text, because a column nobody has confirmed is safe is not safe. |
| `numeric_noise` | no | Moves a number by up to ten percent, keeping its sign, scale, and decimal places, so totals stay the right order of magnitude. |
| `phone` | no | Replaces the digits of a phone number in place, keeping its length, punctuation, and country prefix so that format checks still pass. |
| `postcode` | no | Rewrites a postal code in place, keeping letters as letters and digits as digits so the country's format still validates. |
| `preserve` | yes | Leaves the value unchanged. Use it to record that a column was reviewed and found safe, rather than leaving it out. |
| `string_fpe` | no | Replaces a string with one of the same length, keeping digits as digits and letters as letters so a format check still matches. |
| `url` | no | Keeps a URL's scheme and path shape, replacing its host with a synthetic one at example.test. |
| `username` | yes | Replaces a handle with a unique synthetic one made of a word and a number. |
| `uuid_remap` | yes | Maps a UUID to a different valid UUID. Columns that share a link map identically, so foreign keys still join. |

## Check constraints

A transform has to satisfy the constraints the column already has.

```
AF-MSK-004 Masking would violate the check constraint orders_total_positive on
orders.total.
  Next: Choose a format preserving transform for orders.total that satisfies
  orders_total_positive.
```

`numeric_noise` keeps a number's sign and scale and will satisfy most range
checks. `int_fpe` keeps the digit count and sign. A check constraint that
encodes a business rule, such as a status being one of five strings, needs
`preserve` rather than a transform: there is no synthetic value that satisfies
it and is not the original.

## Choosing one

The question is what a test depends on.

A form that validates a card number needs `credit_card`, which produces a Luhn
valid test number. A layout built for three paragraphs needs `free_text`, which
produces three paragraphs. A report that sums a column needs `numeric_noise`,
which keeps totals the right order of magnitude, rather than `int_fpe`, which
does not.

A column that nothing reads can have `nullify`, and that is the default for
unclassified free text on purpose: it makes the absence visible.

## Uniqueness

```
AF-MSK-007 The transform on users.email produced duplicate values under the
unique constraint users_email_key.
  Next: Use a transform that preserves uniqueness, such as email or uuid_remap,
  for users.email.
```

`email`, `uuid_remap`, `username`, `hash_hex`, `int_fpe` and `string_fpe`
preserve uniqueness. `name`, `city`, `company` and the rest do not, because two
people can share a name and pretending otherwise would mean generating
increasingly unlikely ones to satisfy a constraint the data never had.

## Determinism

Every transform is keyed. The same input maps to the same output within one
golden, which is what makes `link` work and what makes a masked database
self-consistent. Across goldens the key differs, so the mapping cannot be
reversed by diffing two refreshes.

Related: [masking](/concepts/masking/), [verification](/concepts/verification/).
