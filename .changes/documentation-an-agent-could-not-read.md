# added

An agent given the MCP server had no way to read the documentation.

Antifailure is new, so no model carries it. An agent handed `af mcp` could
submit a rehearsal and read a verdict while having no way to find out what a
`stance` is, what the `topology` dimension measures, or why a golden that fails
verification cannot be branched. It guessed, and it guessed confidently,
because nothing told it otherwise.

Three tools now serve the 92 pages this build ships, and the whole design is
about what they refuse to send. `search_documentation` returns the few lines
around a match with the heading path and the anchor that reads that section on
its own, never a page. `list_documentation` names all 92 for 1,416
tokens so an agent can orient before it knows what to search for.
`read_documentation_page` reads one section by anchor, bounded, and a page
longer than the budget is cut at a line boundary, marked where it was cut, and
reported with the exact characters withheld and the anchors of every section
past the cut.

Every response states what it did not return: how many pages matched, how many
were shown, which were dropped and why. A tool that silently truncates leaves a
caller believing it has seen everything, which is the defect this repository
exists to find, and it is worse in a documentation tool than anywhere else,
because the caller has no independent way to know the answer was partial.

The pages are compiled into the binary by `tools/docsembed`, so the answer
matches the build the caller is running rather than whatever is on the website,
and the server needs no network to give it. `just generate` writes the
generated file and CI fails if it has drifted from `docs/src/content/docs`.

Measured by `engine/internal/docs/benchmark_test.go`, which `just benchmark`
runs, and counted with `cl100k_base`: the question "what does stance mean" is
answered in 1,138 tokens end to end, against 262,045 tokens for the
documentation set it was drawn from. Reading the one page the best excerpt came
from would be 6,657, and reading just that section 649.
