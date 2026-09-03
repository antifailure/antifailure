# fixed

`af ci` could not find the agent runner unless the manifest sat at the top of
the checkout or one directory below it. A project kept in a subdirectory, which
is the ordinary shape rather than the exotic one, got AF-AGT-004 naming four
paths, none of which was the runner at the top of the checkout the command was
running inside. `af runner install` already walked up to the top of the checkout
and the search `af ci` uses was a second copy that never learned to. There is
one search now, so the two cannot disagree again, and a run also looks beside
the binary under `share/antifailure` the way the installer already did.

A run that reached no verdict used to give the same reason whether the manifest
declared no workflows or the run died before it reached the workflows it does
declare. The first is true, the second is false, and a false reason sends a
reader to edit a manifest that was never the problem. The report now says which
of the two happened, and when workflows were declared it points at what stopped
the run instead of at the manifest.
