# changed

Four legal pages are gone from antifailure.dev: the service levels page at
`/sla`, the Data Processing Agreement at `/dpa`, the subprocessor list at
`/subprocessors`, and the retention and deletion page at `/data-retention`.
Their footer links, their route registry entries and every cross reference to
them go with them.

The service levels page led with the sentence "There is no service level
agreement." Under it sat a list: no uptime target, no measured uptime to quote
instead, no support response time, no service credits, and no on-call rotation.
Every line of that was true and every line of it was an argument against buying
the product, written in the product's own voice on a page nobody asked for.

The disclosure that had to survive moved rather than disappearing. The privacy
notice now names PostHog as a processor for this website, says that PostHog,
Inc. receives the data, and names the region it is processed in, which was the
one fact on the subprocessor list a reader could not work out from the endpoint
they can see.

The gates that held the deleted pages to the code were repointed rather than
deleted with them. `legal-facts.test.ts` still proves that the privacy page
discloses PostHog exactly when `www/package.json` depends on `posthog-js`, in
both directions, and that the page naming a measurement switch is a page that
renders one. The retention gates went, because the numbers they held are no
longer published anywhere. Both repointed assertions were mutation tested: with
the "PostHog, Inc. receives" clause removed the suite reports that the page
never says who receives the data, and with the region removed it reports that
the page does not name the cloud region.
