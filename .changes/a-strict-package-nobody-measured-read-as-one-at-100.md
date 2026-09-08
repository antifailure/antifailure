# fixed

The coverage gate could not tell a package at 100 percent from one nobody
measured.

`tools/coverage` built its package list from the COVERAGE PROFILE and never
from `thresholds.yaml`, and `thresholdFor` was called only inside that loop. So
a package the thresholds NAME and the profile does not carry was never
iterated, never compared to its floor, and never printed even under `-all`.
Nothing compared the two lists.

`internal/masking` sits in the strict tier at 100 percent, described in the
thresholds file as a package where a missed line is a masking, isolation or
redaction failure. Measured against a profile with that one package removed and
everything else present, the old gate prints

    coverage: 19 packages measured against C.5
    coverage: every package meets its threshold

and exits 0. The package with the strictest floor in the repository had not
been measured at all, and the gate whose entire job is to stop a number being a
claim rather than a measurement reported that as success.

Any tier entry the profile does not carry is now refused by name and by tier,
in the same terms `vulncheck` already uses for a suppression that suppresses
nothing: it is either tests that stopped running, a package that moved, or dead
policy that belongs deleted. The same profile with the package restored still
passes, so the refusal cannot be satisfied by refusing everything.
