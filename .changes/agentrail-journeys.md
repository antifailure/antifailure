# added

We could not see where an AI agent trying Antifailure got stuck. A person who
gives up on the docs at least shows up in the page counter; an agent that reads
`llms.txt`, signs in through the device flow and then stops after two MCP calls
looked like nothing at all, because nothing joined those steps together.

The site and the hosted control plane now report those steps to Agentrail.

The site bundles `agentrail-browser` behind the same switch PostHog already
sits behind, so Global Privacy Control, Do Not Track and the switch on the
privacy page all keep it from loading. It sends a page load's address without
the query string, the time and the browser identification string, and sets one
cookie, `agentrail_session`, which ends after thirty minutes idle or a day after
it began. Behaviour timing, the fingerprint and WebMCP payloads stay off. The
privacy page and the data boundary page say all of that, including the one
place it differs from PostHog: switching it off sends the last few seconds it
already queued rather than discarding them.

The control plane reports only when `AF_AGENTRAIL_SECRET` and
`AF_AGENTRAIL_HASH_KEY` are both set, refuses to start on one of them, and
records only an allowlist of request paths: signing in, the OAuth and device
routes, and `/mcp`. It records no outbound call at all. That second half is
what actually holds the boundary: the SDK's default setup records every
outbound call made while serving a recorded request, and signing in calls
GitHub at `/orgs/<organization>/memberships/<login>`, so an allowlist on its own
would still have sent customer organization names. The signed in user goes as
a keyed hash, and an MCP call as its method, tool name and client, never its
arguments or result. A self-hosted control plane sends nothing.

The 404 page and `llms.txt` also invite an agent that got stuck to say so, at
`feedback.agentrail.co/p/antifailure`.
