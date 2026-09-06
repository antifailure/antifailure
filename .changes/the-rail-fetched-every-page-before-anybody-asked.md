# fixed

The console fetched the payload for every page in its sidebar the moment any
page rendered.

Next prefetches each link it can see, and the rail holds a dozen of them, so
one page load fired twenty two requests for segment files named like
`/load/__next.!KGFwcCk.load.__PAGE__.txt`, each carrying the encoded route
group in its path, before the person had touched anything. Somebody reading
the network panel on the Plan page saw those and asked what they were doing
there, which is a fair question with no good answer.

The rail's links now fetch a page when the pointer or keyboard focus reaches
them, which is long enough before the click that navigation still feels
instant, and only for the one link about to be used. Measured on the built
console behind a stub session: twenty two requests on load before, two after,
both of them the current page's own, and hovering one entry fetches that entry
alone.
