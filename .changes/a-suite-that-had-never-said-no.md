# fixed

The database conformance suite had never been shown to fail.

Its own package doc said so. Twenty four behaviors were declared, every
provider ran them, every run printed ok, and nobody had ever watched one of
them go red. A suite in that state is a list of assertions that might all be
vacuous, and the ways an assertion goes vacuous are undramatic: a helper starts
skipping, a comparison compares a value against itself, a body sits inside a
condition that is never true. Every one of those still prints ok.

Each behavior now has a break that turns it red, and the self test fails if one
of them stops going red, if a behavior passes by skipping, or if a run matches
no test at all, which exits zero and reads exactly like a pass. Nineteen are
proved with no infrastructure, because a negative control that needs a server
gets skipped and a skipped negative control is a false green. The other five
are about isolation, reset, and what a branch actually holds. Those are claims
about bytes, so they run against real databases on a real Postgres, and CI
already sets AF_REQUIRE_DATABASE so that its absence fails the job rather than
skipping it.

Doing it found two behaviors asserting something other than what they claimed.
Cancellation_LeavesNoUntrackedResource had its polarity inverted in both
directions: it failed a provider that reported the resource it created, which
is the correct thing to do, and it never looked for a resource created and not
reported, which is the leak the rule is about. Its message said "that the
caller has no identifier for" while its code required the caller to have one.
Branch_RefusesAnUnverifiedGolden could not reach the branch side of its own
rule, because a provider that correctly refuses to publish an unverified
version never produces one to hand to Branch; the fakes can now flag such a
version rather than withhold it, which the suite always permitted, and a
control proves that affordance alone leaves every behavior green.
