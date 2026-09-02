# changed

The Reference group is numbered 1 to 7.

It was 1, 2, 6, 7, 8, 9, 10, with four numbers unused, because when the group
was first renumbered two of its pages belonged to another agent and asserting
contiguity would have forced an edit into files that were not mine. Those files
are mine now, so the gap closes.

Nothing moved. The rendered sidebar is byte-identical before and after, which is
the whole point of the change: it tidies the record without touching what a
reader sees.

While doing it, a real hazard appeared that the change itself created.
Reference and Self-hosting are now listed by hand in `astro.config.mjs`, and a
hand-listed group renders in the order of the ARRAY, so those pages'
`sidebar.order` is inert. Two descriptions of one thing is the shape this
repository keeps finding defects in, and the inert one is the dangerous half:
somebody renumbers frontmatter, nothing moves, and they conclude the sidebar is
broken. `just sidebarcheck` now requires the two to agree, so the frontmatter
stays meaningful as the record of intent and as what would render if the group
ever went back to autogenerate. Watched failing by swapping two entries in the
config.
