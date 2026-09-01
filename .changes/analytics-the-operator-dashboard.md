# added

The analytics dashboard in the console, and the one gate on this server that is
not a permission. The page answers about the whole installation rather than one
organization, and every organization has an owner who holds every permission in
their own, so the route requires membership of the organization named by
`AF_ANALYTICS_OPERATOR_ORG` as well as the `analytics.read` permission. Unset
means nobody, and the route says which variable to set.

Every panel carries where its numbers came from: the window, when the rollup
last ran, which days are still moving, and whether recording is switched on at
all. Three different things render as zeros and the page says which one it is
showing.
