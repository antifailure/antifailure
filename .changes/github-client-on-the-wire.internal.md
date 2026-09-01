# added

`RealRepositoryApi` had no test of its own. The lifecycle suite drives a fake
that enforces the rules, which is the right thing for orderings and cannot
exercise the half that is only ever wrong on the wire: a path assembled with the
wrong number of segments, a 409 read as a failure, a page of comments discarded
because one element decoded badly.

It now runs against a local HTTP server answering the shapes GitHub answers,
including the four that are easy to get backwards: a 403 that means a permission
rather than a missing resource, a 404 on a cancel, a 409 on a cancel that means
the run already finished, and a 409 on a re-run that means it has not.
