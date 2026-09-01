# fixed

The runtimes table on Environments and the approval queue on Network showed no
column headings on a phone.

A table stacks into one record per row below 640px and repeats each column's
name from the `label` its cell was given. Eleven of fourteen tables pass one.
These two passed none, so at 390px the approval queue read `api.stripe.com`,
`MOCK`, `acme/checkout`, `ada-490360`, `12m ago`, `Approve`: six bare values on
the screen where somebody approves an egress rule. In both cases a correctly
labelled table sits inches away on the same page.
