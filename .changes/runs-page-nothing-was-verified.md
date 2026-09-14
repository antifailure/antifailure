# fixed

The runs page could not tell a run that proved nothing from a clean run. Both
`unverified` and `flaky` are real verdicts the runner returns, and neither was
in any of the colour function's three lists, so both were drawn in the grey this
console uses for a value nobody needs to react to. A run whose personas all
failed to provision therefore filled the verdict table with rows, showed no
failures anywhere, and read as a pass.

Those two verdicts are now amber, a run whose verdicts judged nothing carries a
banner saying so with the counts beside it, and the empty state no longer tells
a finished run it is probably still going.

The same page also never updated once it was open: the only refresh in the file
hung off the Start card, which a member cannot see, so a run could not be
watched to completion in the console this product is demonstrated in. The list
and the detail now refresh themselves while a run is queued or running, and stop
the moment it finishes.

And the reproduction recorded for a failing verdict, which the control plane
selects and sends and the operator console has printed all along, is now on the
page the customer is shown rather than fetched and discarded.
