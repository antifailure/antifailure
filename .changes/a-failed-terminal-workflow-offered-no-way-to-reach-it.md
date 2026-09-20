# fixed

A terminal workflow that failed reported a verdict with nothing under it.

The report skips its "how to see this yourself" block whenever a workflow's
steps are empty, and the terminal driver handed it an empty list: the shared
classifier cannot write one, because what a person would have to DO to see a
result again is a fact about the surface rather than about the verdict, and
only the browser driver was filling its own in afterwards. So a red check named
a terminal workflow, gave a one line reason, and offered no way whatsoever to
reach it.

It now carries the same thing a failed browser workflow carries: the
invocation, the size of the terminal it was given, one line per key that was
pressed, what was expected, and what happened instead.

The rendered screens stay out of it on purpose. That block is markdown, and
markdown collapses the runs of spaces that hold a screen's columns together, so
a grid of cells would arrive as something nobody could read. The screens are in
the run's own steps, which is what `af watch` prints live and what the JSON
report keeps whole.
