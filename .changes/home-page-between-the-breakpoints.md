# fixed

The home page was built for a phone and for a wide desktop, and rendered as
neither in between. Every layout decision on it switched at `xl`, so a 1100px
browser window, which is a laptop rather than an edge case, got the phone
treatment at desktop measurements.

The hero's five service cards were a horizontal scroller below `xl` with its
scrollbar hidden. At 1100px only three of the five were on screen, the fourth
was cut mid-word at the right edge, and nothing said the other two existed:
372px of the row sat outside the viewport with no affordance at all, and a
sideways trackpad swipe over it moved the row, which reads as the page
scrolling sideways. It reflows now: five columns above `xl` unchanged, three
from 1024px to 1279px, two from 768px to 1023px, and the scroller only below
`sm`, where a card is 78vw so the next one always peeks and a sideways swipe is
the gesture a phone expects anyway. All five cards are fully on screen at every
width from 640px up, measured, and the row's hidden scroll is zero.

The aurora behind the hero ended in a torn horizontal edge. Its frame is a
pixel height per breakpoint while the hero's height moves continuously with how
the headline wraps, so the two only agreed at the widths the art was tuned at.
Between 1100px and 1279px the headline drops from three lines to two, the
service row rises 68px, and the frame's bottom edge landed inside those
paragraphs: body text lay across a single-row luminance step of 36 out of 255.
The same edge cut the "Get started" button in half at 375px, where the step was
100. The hero's own bottom gradient could not cover either, because it is
anchored to the section's bottom and the frame ends where that gradient is still
transparent. A second fade is anchored to the art's own bottom edge now, on both
the desktop frame and the phone copy, so the band dissolves into the page ground
wherever the edge falls. Measured at ten widths from 320px to 1440px: the step
across that edge was 23 to 100 before and is 0 to 1 after.

Section headings were sliced in half between 1024px and 1279px. The sticky
section rail pinned itself 64px from the top while the header is 56px tall
below `xl`, not below `lg`, leaving an 8px slot between the two that every
heading scrolled through. The rail is `xl:hidden`, so it only ever exists where
the header is 56px and the breakpoint on its offset was wrong in both
directions; the constant behind its scroll-to targets carried the same mistake
and was two pixels short. Gap measured at 0 from 320px to 1100px.

The logo was the last control on a phone under 44px, at 32px beside a menu
button already at 44px. Its contents are centred, so the rendered header is
byte-identical at 375px, 1100px and 1440px and only the hit area moved.

Two more controls were smaller on a phone than on a desktop, which is the wrong
way round. The primary button carried `max-lg:h-9 max-lg:text-sm`, so below
1024px the site's main call to action shrank from 44px to 36px and its label
from 16px to 14px: Get started, Read the docs, Join the waitlist and every other
one, on every page, only ever under 44px on the viewport where a thumb has to
hit it. The height and the type are the same at every width now and the narrower
horizontal padding below `lg` stays, because that is what keeps two buttons on
one line. Measured 44px tall with 16px type at 320, 375, 414, 640, 768, 900,
1023, 1024, 1100, 1280 and 1440.

No page on the site scrolls horizontally at any width: 23 routes times ten
widths, checked against the static export with the check proved able to fail.
