# fixed

The documentation offered no way to sign up on a tablet.

Between 800px and 1023px wide, no documentation page carried a Log in link, a
Sign up button or a link to the repository. The header hid all three at `lg`,
and the mobile menu that holds their only other copies exists only below 50rem,
so for 224 pixels of viewport width they were hidden with nowhere to go. The
footer of a documentation page carries Privacy and Terms and nothing else. iPad
portrait is 810 to 834 pixels.

Three different collapse boundaries caused it: the navigation at `md`, the
buttons at `lg`, the menu at 50rem, in a file whose comment said the navigation
collapsed at 50rem while its class said `md`. Both are 50rem now, matching the
menu, and only the GitHub link still waits for `lg`, because measured at 800px
the row is 890 pixels wide with it and 749 without.

Three smaller things in the same bar. The Docs link claimed `aria-current="page"`
on all 82 pages, so a screen reader reading the command reference was told it
was on the Docs page; it now says `page` on the documentation home and `true`
everywhere else. It pointed at `/docs/` while the marketing header points at
`/docs`, so the site's own Docs link was the one piece of navigation that cost a
redirect. And the GitHub link stood 18 pixels tall, under the 24 pixel minimum.

The focus ring on those links faded in rather than appearing. Tailwind v4 added
`outline-color` to what `transition-colors` animates, so the ring interpolated
from each link's own colour over 200ms: on Sign up that start colour is white on
a white header, so the indicator was invisible for the first hundred
milliseconds of every focus. Measured at 0, 50, 150 and 300ms before and after.
Colour is out of that transition now and hover still animates.

Starlight names its sidebar landmark "Main" and the header above it has a nav
named "Main", so from 800px up a landmark list held two identical entries. The
sidebar is "Documentation".
