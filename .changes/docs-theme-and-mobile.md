# docs

The documentation looked like a different product from the site around it, and
its code blocks were close to invisible on a phone.

A code block was painted `#f2f2f0` on a `#ffffff` page: a contrast ratio of
1.12:1 for the fill and 1.12:1 for the border against that fill, so it had
neither a visible surface nor a visible edge. Code is now dark, on the site's
own `#18191b`, which measures 16.4:1 against the page; every token in the theme
is checked against that background and the lowest, the comment colour, is
6.87:1. Wide lines still scroll inside the block and now say so, with the same
scroll shadow the reference tables use.

The header was a copy of the site's that had drifted from it. The logo mark was
18px against the site's 24px and the wordmark 15px against 16px; the bar stood
64px tall from 1024px up while the site drops to 56px below 1280px, so at
1100px the two headers were different heights; "Writing" was missing from the
navigation; the container changed its side padding at a different breakpoint;
and the leading gap had the wide value at the narrow end, which is the bug the
site itself fixed and left a comment about. The header now matches at every
width measured: 56px below `xl` and 64px above it, same mark, same type, same
padding, same links. The page also takes the site's `#f7f7f5` ground instead of
pure white, and the selection colour matches.

On a phone every control is now at least 44px. The sidebar toggle was 32px, the
search button 40px, each table-of-contents row 34px, the anchor link beside a
heading 24px wide, and the footer links 30px tall. The toggle also lost the
white circle and drop shadow that made it read as a button floating over the
page rather than a control in the bar, and the search control fills its row
instead of collapsing to one square in an otherwise empty band. Below 50rem the
site's own navigation moves into the sidebar menu, so no destination is
unreachable when the header collapses.

The rule above every `h2` was `display: inline` and therefore only as wide as
its own text: 146px in a 343px column. It now spans the column, which is what it
was for. The footer's copyright measured 4.13:1 and is now 5.9:1, the page
carries two corner radii instead of six, and no page scrolls sideways at 375px.
