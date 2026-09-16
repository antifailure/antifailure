# added

An agent editing code had no cheap way to ask Antifailure whether the change it
had just written contained a bug. The static, model-backed code reviewer already
existed, but the only ways to reach it were to submit a whole rehearsal, wait
for an environment that a correctness bug does not need, and then read the
finding off a run, or to open a pull request and scrape the comment the reviewer
left on it. Both put the fastest, cheapest signal the product has, a model
reading the added lines, behind the slowest machinery it owns.

The review_change MCP tool exposes that reviewer on its own. It reads the diff,
not the twin, so it opens no session, builds no image and needs no database:
mid-edit, an agent asks it and gets the concrete defects the model found on the
lines the change added, each with a rule, a category, a level, a bounded
description, a fix and a file:line into the agent's own changed code. A location
is allowed here, unlike a security finding, because it points at the caller's
own diff rather than a copy of production, and the projection still refuses to
carry anything shaped like a captured body, response or row, because a diff has
none. A change that touches no code is reported as nothing to review rather than
a clean pass, and a change with code but no model key configured is reported as
skipped with the reason, because a review that did not run is not a review that
found nothing. The finding's level is the project's own policy, so it advises by
default and never blocks a merge on a model's say-so.
