# fixed

The documentation build warned on every run that `/404` was declared twice, and
Astro's own message says a route collision becomes a hard error in a later
version.

This site ships its own 404 at `src/pages/404.astro`, and Starlight injects one
at the same pattern unless told not to. `disable404Route: true` tells it not to.

Ours is the one that should win, and not because it was there first. Somebody
who lands on it arrived from a path printed at the end of an engine error
message rather than from a link, so the page names the four references they were
most likely reaching for and tells them the address in their terminal is the
wrong half. Starlight's default is a generic page with none of that.

What this does NOT change is what gets served: the built `404.html` was already
ours before and after, byte for byte the same page. The warning was about which
of two declarations Astro would honour being undefined, not about it having
picked the wrong one. That is worth stating plainly, because "we were already
getting the right answer" is exactly the reasoning that leaves a collision in
place until the version that turns it into an error.

Route collision warnings in a full docs build: 2 before, 0 after. The six
remaining vite warnings are about self-hosted font files resolved at runtime,
are unrelated, and are untouched.
