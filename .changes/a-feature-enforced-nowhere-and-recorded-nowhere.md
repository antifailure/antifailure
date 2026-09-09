# fixed

Record the two enterprise features that a license names and nothing gates, and
add the check that could have found them.

Twelve features can be named in an enterprise license. Seven are enforced at a
real site, two are refused at issue and at evaluation because nothing implements
them, and three were neither. Two of those three said so in prose. `air_gapped`
said so nowhere: every occurrence of the name in the repository was a copy of
the catalogue, the license vectors, a line of documentation, or a test. A
license naming it verified, reported itself active, printed in
`af license status`, and granted nothing, which from outside is what a working
feature looks like.

Nothing could have caught it, and that is the more useful half. The control
plane's registry test asserts that the features enforced THERE have declared a
site, and correctly declines to speak for the engine. The engine's own registry
test asserts a site set is empty from a test binary that links none of the
enforcing packages, so it cannot fail. Between one check that will not look at
the engine and one that cannot look at anything, nobody ever asked whether every
feature does something somewhere.

`unenforced` in `ee/engine/license/license.go` now records a feature that ships
and is gated nowhere, with the reason stored beside the name, the way
`notShipped` already records one that does not ship at all. The two are
different answers: absent cannot be sold, a feature we do not gate can be, and
only the first is
a reason to refuse a request. So `tools/licensegen` warns rather than refuses,
naming the feature beside the key it just signed, and permits nothing new and
refuses nothing that used to work.

The check that spans both halves lives in the control plane's catalogue test,
which already reads `license.go` and `licensegen` as text because no import
joins them. It requires every feature to be refused, recorded as unenforced, or
enforced at a site it can find in the engine, the control plane, or the
community entitlement catalogue. Each of its three scanners carries a positive
control naming a feature it must find, so a scanner that has stopped reading its
input says so instead of quietly reclassifying a feature nothing gates as
enforced.
