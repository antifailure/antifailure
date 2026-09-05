# Careers and a readable application queue

The public page hires founding engineers at 0.5-2% equity and founding growth
at 0.25-2%. Both have no salary currently. It makes no promise about when salary
starts, vesting, location, funding, or employment classification.

A dedicated application form is preferable to an email link, which cannot
confirm delivery, or the sales form, which has a different purpose. It collects
name, email, role, an optional work URL, a short explanation, and acknowledgment
of the current compensation. It asks for no demographic data or attachments.

The public endpoint writes a separate recruitment table. An opaque submission
key makes a retry of the same payload safe without exposing who has applied.
Origin restrictions, bounded input and an ephemeral rate limiter constrain
abuse. Application content is not sent to analytics or notification logs.

An operator Applications page lists the oldest unreviewed submissions first,
can mark them reviewed, and can delete their personal data. All operations use
the existing operator authentication, authorization and audit path. Scheduled
maintenance removes applications older than 180 days. The form discloses this
retention and does not promise a response or an email it cannot prove was sent.

Verification covers the actual public submission, database restrictions,
operator read and review, retry/concurrency, deletion, retention, validation,
and failed writes. Each new assertion gets an independent production mutation.
The rendered public form and operator page are inspected at mobile and desktop
widths, including keyboard, empty, error, sending and confirmed states.
