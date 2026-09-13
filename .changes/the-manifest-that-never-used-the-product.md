# changed

This repository's own manifest now rehearses the pages a customer crosses on
the first day, and two defects that stopped it from doing so are fixed.

The manifest had eight workflows on a handful of signed in pages, one
exploration goal, no oracle probes, no load scenarios and nobody signed out.
`compare_with_previous_release` refused to run against this repository,
`af load scenario` answered AF-LOD-010, and the sign-in screen, the getting
started steps, the plan, the terminal sign-in, the members list, settings and
the operator portal were visited by nothing. It now declares a `visitor`
persona with `login: none`, seventeen workflows, ten exploration goals, an
oracle block of eighteen signed out probes and two ordered load scenarios under
`scenarios/`, and every one of them was run against a real environment before
it was committed.

A persona with `login: none` stopped provisioning for every persona. The
engine handed it to the authentication adapter like any other, and a seed
command written for accounts that sign in refuses one with no address, so
`af up` reported that nobody could sign in and `af test` and `af ci` failed
before a single workflow. Such a persona is now recorded without the adapter
being asked to create anything, since it never signs in.

An exploration could pick a control nobody could press. The snapshot offered
elements hidden with `display: none`, such as a mobile menu button at desktop
width, and pressing one timed out and ended the exploration as blocked with
nothing explored. Hidden controls are no longer offered.

The burst scenario is sized to the control plane's own limit of twenty page
loads a second per address, because every session a scenario sends shares the
load generator's one address. Forty sessions measured the limiter refusing 392
of 640 requests rather than the console serving them.
