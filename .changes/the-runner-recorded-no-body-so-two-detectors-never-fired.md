# added

The runner now captures response bodies, the rendered DOM, and every request it
reached, so two detectors that were wired but starved can fire on a real run.

The browser recorded no response body and no DOM, and it logged only the GET
pages it navigated to. Two consequences followed, both silent because the engine
consumers were already in place and simply handed nothing. canary_leak scans the
run's DOM and response bodies for a secret by shape, a Stripe key or a private
key that reached a response; those streams were always empty, so even a bundle
that shipped a live key to the browser produced no finding. And the injection
family fuzzes the routes the run observed reaching; with only GET navigations
recorded, a POST, fetch or XHR API route was never sourced, so the write path
where most real SQL injection lives was never exercised.

The runner records, per same-origin response, the request that reached the
server with its method, so a POST or fetch route is fuzzed as the method it was,
and the bounded body of each document, JSON or text response, so a leak family
has something to scan. It captures the rendered text of each page it stood on
for the same reason. Everything is bounded in count and in bytes, a cross-origin
third party's body is never captured, and a binary or oversized response is
dropped before it is read. The bodies and the DOM stay inside the engine as
evidence: a finding reports a location and a kind, a route carries a method,
a path and parameter names, and neither ever carries a captured value.
