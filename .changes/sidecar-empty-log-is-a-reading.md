# fixed

An application that made no outbound call was reported as one nobody could
observe. The sidecar's log was read, it held no decision and no captured
message, and both parsers answered with nothing at all rather than with an
empty list. Every reader of those answers takes nothing to mean the log could
not be read, so `af ci` said the `ssrf` and `side_effect` security families
could not complete and reached no verdict, and the MCP egress inspection told
an agent that no environment was running for the branch while one was, with
the verdict `INCONCLUSIVE`.

A log that was read now answers an empty list, and only a sidecar that is not
there answers nothing. On an application whose policy blocks everything and
which calls nothing, both families now look and pass, and the MCP inspection
reads `PASS` with zero decisions observed. A run whose logs could not be read is
still refused by both families, exactly as before.
