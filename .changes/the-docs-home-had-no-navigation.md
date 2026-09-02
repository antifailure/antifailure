# fixed

The one page a new reader lands on was the only page in the documentation with
no way to see what else exists.

`docs/src/content/docs/index.md` carried `template: splash`, which is
Starlight's marketing template and turns the sidebar off. Below 800px it had no
menu button either, so on a phone the documentation home had no navigation at
all. It rendered its `title` frontmatter, the single word "Antifailure", as a
display headline that told the reader nothing, above a lede and two buttons in a
column with the right half of a wide viewport empty.

It is a documentation page now. The sidebar, the table of contents and the menu
button are all back because they were never optional, and the page is written as
entry points: the three ways to run this, the ideas the rest depends on, what is
gated against its source and what is not, and how to hand the whole thing to an
agent. Hairlines and space rather than shadowed cards, one accent, and the type
scale every other page already uses.

The site footer was confined to Starlight's measure column while the site header
above it runs the full width, so on a phone it was a 358px rule under 358px of
text and read as one more block of content rather than as the bottom of the
page. It breaks out to the pane now and pads itself back in, so the rule spans
and the copyright still sits on the exact left edge of the paragraph above it.
Measured at 42 widths from 280 to 1920 before and after.

# added

Every documentation page is now available as Markdown, and there is a control
that hands it to you.

`/docs/llms-full.txt` has served the entire corpus, 665 KB across all 81 pages,
since the first build, and nothing on the site or in the documentation linked to
it. The most useful thing this site can give an assistant was built on every
deploy and reachable only by somebody who already knew it was there. That is the
dead-capability shape with the last step missing.

So: `docs/src/pages/[...slug].md.ts` serves any page at its own address with
`.md` on the end, which is guessable from the rendered URL without being
documented. The bar at the top of every page carries a Copy as Markdown button
and a link to that address. The button fetches the same route the documentation
tells an agent to use, so a broken route cannot pass in the browser and fail for
the agent. And the documentation home now names the corpus, the index and the
per-page form in one place.

`claimcheck` learned that `/docs/<page>.md` resolves exactly when `<page>` does,
because that is what the route's `getStaticPaths` does. Checked that the
widening did not cost coverage: a twin for a page that does not exist is still
reported as a 404.
