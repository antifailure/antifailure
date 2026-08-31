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

Two local traps cost time on this change and are recorded so they cost nobody
else any. Both produce an error that points at the wrong file.

An install that is stale against its own lockfile lies about the cause. `docs/`
had Starlight 0.36.3 against a lockfile pinning 0.41.10, and the build failed
with "Invalid config passed to starlight integration" naming every sidebar
group. The config was correct and `npm ci` fixed it. `www/` had Next 15.5.23
against a lockfile pinning 16.3.3, which is the more dangerous direction: a
stale major masks a real break rather than causing one. Run `npm ci` in any
workspace before concluding anything about its source.

Astro keeps a stale reference to the Expressive Code stylesheet across a change
to the `expressiveCode` config, so the HTML links a hashed CSS file the build no
longer emits. The 404 is silent, and because the frame's own `overflow-x` lives
in that stylesheet, code blocks render completely unstyled and one wide line
pushes the whole page sideways on a phone. It is indistinguishable from a theme
that was never applied. Any change there needs
`rm -rf dist .astro node_modules/.astro`, and the way to confirm it is to
compare the `ec.<hash>.css` the HTML asks for against what is in `dist/_astro`.
There is a comment saying so above the config itself.
