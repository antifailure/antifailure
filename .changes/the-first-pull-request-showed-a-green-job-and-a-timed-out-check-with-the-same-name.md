# fixed

The first pull request after installing the GitHub App showed a green job named Antifailure beside a check named Antifailure that waited forty five minutes and then said nothing was verified.

The App posts its check the moment a pull request opens, and the check
concludes when the workflow reports back through the control plane. The file
the App committed reported only when the repository variable
`AF_CONTROL_PLANE` was set, the pull request body called that variable
optional, the console never named it, and nothing on the path set it. So the
workflow commented for itself, its job went green under the same name as the
check, and the check read "Waiting for a runner" until the deadline sweeper
wrote `timed_out`. A branch protection rule requiring "Antifailure" could not
tell the two apart.

The file now carries the control plane's address as the variable's default,
and the App writes its own address there when it opens the pull request, so a
self hosted control plane's file reports to it and the hosted one's file is
the example unchanged. The pull request body, the console's onboarding, `af
github init` and the documentation say the address is in the file and there is
nothing to set, and that the variable exists to point the run at a self hosted
control plane. The reusable workflow's job is now "Antifailure rehearsal",
which reaches customers when `v1` next moves, so the verdict and the runner no
longer share a name. And when the repository's own Antifailure workflow
finishes a pull request run without ever having claimed the commit, the check
concludes then, with a sentence that names the variable, this control plane's
address and the documentation page, rather than saying nothing for forty five
minutes. A skipped or cancelled run of that workflow, a dispatched run of it,
and any other workflow in the repository still conclude nothing.
