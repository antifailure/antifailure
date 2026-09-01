# fixed

A workflow whose Stripe call was invented by a model reported PASSED.

The promise is made in five places, including the product page and the comment
at the top of the proxy's own synth handler: a workflow that touches a
synthesized response reports unverified rather than passed, because what it saw
came from a model rather than from the thing under test. It was kept nowhere.

Three separate breaks in one chain, and each one alone was enough.

The sidecar wrote `synthesized: true` on the decision and set an
`X-Antifailure-Synthesized` response header. `local.Decision` had no field for
it, so `json.Unmarshal` dropped it silently, and every consumer downstream saw
a synthesized call as an ordinary allowed one. `pack` and `fixture` were
dropped the same way, against the sidecar's own comment that a mock which
cannot name its fixture is a mock nobody can debug.

The runner's `synthesized-response` cause did map to unverified. Its only
producer fired when nothing on the page confirmed or contradicted an
expectation, which is a page nobody could read and has nothing to do with a
synthesized response. So the one cause that produces unverified was already
spoken for, and the real case had a mapping, a test, and no producer at all.
That branch is `page-unreadable` now, which is what it always was, and the two
have different remedies: a model key for one, a sandbox credential or a fixture
for the other.

And nothing connected the two halves. The runner drives a browser; a
synthesized call is made by the APPLICATION, server side, and never appears in
anything a browser can see. Only the proxy knows and only the engine reads the
proxy, so the engine is where the verdict is now decided. The runner emits the
window a workflow ran in, and a synthesized decision inside it downgrades that
workflow's pass to unverified, naming the hosts.

Attribution is by window and honest about its limit: a synthesized call inside
no workflow's run is reported as a note on the run rather than pinned on
whichever workflow was nearest. A failure is never downgraded, because the
application doing the wrong thing with an invented answer is a real finding and
hiding it behind our own escape hatch would be worse than the bug.

`af net log` says which responses were invented and which fixture answered a
mock, both of which it had been recording and not showing.
