# fixed

The data export and account deletion page has never rendered. It read
`account.exits`, the control plane registers `account.context`, and nothing in
this repository could see the difference: a console path is an ordinary string,
so a name with no procedure behind it is a 404 at run time rather than a
compile error. Every visit to the one screen a lapsed customer is sent to drew
an error card instead, and that screen is also the nav entry, the primary
button on the lapsed plan and the wordmark target for anybody who cannot bill.

Behind it, a second defect that fixing the first would have exposed: the page
typed `sessions` as always present and read its count in exactly the branch the
server leaves null, which is every member and every viewer.

`tools/routecheck` now reads console call sites as well as the marketing site,
so a console page naming a procedure the control plane does not register is
refused before it merges.
