# fixed

CI linted the engine and nothing else.

`tools/` holds prosecheck, gatecheck, changecheck, routecheck, wirecheck,
claimcheck and every other instrument this repository trusts to say no, and no
workflow had ever pointed a linter at it. Held to the same set the engine is
kept at zero on, it carried 122 findings.

The count matters as much as the fix. The first look reported 27, and 27 was
the number a plan was built on. golangci-lint caps its output by default at
three of any repeated issue and fifty per linter, so the run that said 27 had
seen 122 and printed a sample. Both caps are now off, in the shared config, so
a future regression is reported at its real size.

Every finding is fixed rather than suppressed and no rule is disabled. The
prints that deliberately ignore a failed write to a report stream now say so at
the call site, in the form the engine already uses. Deferred closes on read
handles take the engine's idiom too. Two format verbs that dropped a wrapped
error, three error strings ending in punctuation, a deprecated parser call, a
hand-rolled type assertion on an error, and two dead test helpers are all
corrected.
