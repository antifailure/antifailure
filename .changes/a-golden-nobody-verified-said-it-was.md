# fixed

`ListGoldens` reported every golden as verified, including the ones that were not.

`RefreshGolden` records `Verified: spec.Verify != nil`, which is a real value: a
refresh with no verifier commits an image and says so. `ListGoldens` then built
every version with `Verified: true` unconditionally, under a comment reasoning
that a committed image only exists because verification passed. Half of that is
true, since a verification that FAILS never commits. The half that is not is
that a refresh with no verifier commits too.

`pickGolden` reads the listing, not the refresh. So the honestly recorded
`false` was overwritten by the read, and `af up` branched an unverified golden
exactly as if it had been checked, with nothing printed anywhere.

The verified state is now read from the attestation label rather than assumed.
That label is written at commit time and is only ever produced by a verifier
that ran and returned, so it is the durable record the read was missing: no new
label, no migration, and a golden that was verified stays verified. One that
never was now says so, and costs its owner the single refresh `pickGolden`'s
own comment describes as the price of refusing.

The case that hid this had no test. The round trip test published a golden WITH
a verifier and asserted `Verified` was true, which an unconditional true
satisfies perfectly. There is now a test for a refresh with no verifier, which
is the only shape that can tell an honest read from a hardcoded one.
