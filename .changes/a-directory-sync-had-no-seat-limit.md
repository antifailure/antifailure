# fixed

Directory provisioning now refuses an addition past the licence's seat count, so
a SCIM sync cannot add members without bound on a licence sold with a seat
number.

AF-EE-004 has said "the license covers N seats and they are all in use" since
single sign-on took a seat limit, and `ee/web/sso` refuses the addition rather
than making room by removing somebody. Directory provisioning took no seat limit
at all. The word "seat" appeared nowhere in `ee/web/scim`, and the enterprise
entry point constructed `scimExtension` with no such option while handing
`ssoExtension` the licence's own number. So the two provisioning paths disagreed:
one enforced the count and the other ignored it, which makes the limit a limit
with a documented way around it.

`scimExtension` now takes the same seat function, supplied from the same licence
claims, and asks it per request rather than capturing a number at startup, so a
licence that grows or lapses takes effect on the next provisioning call.

The refusal is on the addition, always, and it sits where a directory actually
adds a person: creating an active resource, and reactivating a deprovisioned one,
which is an addition too and would otherwise be one PATCH away from evading the
count. An inactive resource takes no seat, because it is a profile the directory
owns rather than somebody who can sign in. No seat count means unlimited, which
is what an unmetered licence is and what the engine already reads zero seats as.
The refusal is a 403 rather than a conflict: a provider retries a conflict and
reconciles it, and there is nothing to reconcile until somebody buys a seat or
removes a member.
