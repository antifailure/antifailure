# fixed

Every h2 in the documentation pushed its anchor link onto a line of its own,
67px below the heading, on all 81 pages.

Starlight renders a linkable heading as a wrapper div holding the heading and
the anchor link as siblings, and its own `anchor-links.css` sets the heading to
`display: inline` so the link sits at the end of the last line. This site's
stylesheet set `display: block` on the h2 to make the rule above it span the
column rather than the width of the heading text, which it did, and which also
turned the anchor into the next block in the flow.

Cascade layers are why the override was silent. Starlight ships its CSS in
`@layer starlight.content` and this stylesheet is unlayered, so a plain
`.sl-markdown-content h2` beats a layered rule whatever its specificity, and the
usual reading of a diff for "does this win" gives the wrong answer.

The rule now sits on the wrapper, which is already a block, so it stays the full
column width and the heading stays inline. No `margin-top` was moved with it,
deliberately: Starlight gives the wrapper 1.5em, which is what renders today
because the heading's own 2.75rem collapses into it and loses, and restating
2.75rem on the wrapper would have won and tightened every heading on the site by
8.5px. The h3 rule was deleted rather than relocated for the same reason.

Measured before and after against the same build, at 1280 and 390 pixels on the
manifest reference, the agents concept and the quickstart: the gap above every
h2 is 52.5px and 43.5px in both, the rule is 656px and 358px wide in both, the
gap above every h3 is 43.5px and 36px in both, and the anchor moved from 24 of
29 headings off their line to none of them.
