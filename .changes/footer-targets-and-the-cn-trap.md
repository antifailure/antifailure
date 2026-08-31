# fixed

Every interactive target in the footer now measures at least 44 by 44, on every
page at every width, and on a desktop the footer renders byte for byte as it did
before.

It was the last surface on the site below the minimum, and it was the whole
surface: six legal links 20px tall and as little as 25px wide, twenty eight
navigation links 29px tall, and the mark 28 by 28. The three needed three
different answers, because a hit area can only grow into space that is there.

The mark sits alone in its grid cell, so `p-2 -m-2` buys 44 by 44 and gives the
space straight back. The legal links have room above and below the row, so
`px-2.5 py-3 -mx-2.5 -my-3` does the same; the 10px of side padding is picked
rather than round because DPA needs 19px to reach 44 and the column gap is 20px
at 375, so two neighbours meeting in the middle of it end up exactly touching.
On one line, which is every width from 768 up, nothing about that row moves. On a
phone it wraps, and two wrapped lines 8px apart would have overlapped by 16px, so
the row gap is 24px now and the footer is 16px taller at 375.

The twenty eight navigation links could not take that treatment and it is worth
saying why, because the diff would have looked identical and the bug would have
been invisible. They sit flush: the row is 29px and the gap between rows is zero.
Padding with a negative margin would have given each link a 44px box overlapping
its neighbour by 15px, and since the later sibling paints on top, every link
would have lost its bottom 15px to the one after it. A screenshot would not show
it and every target would have measured 44.

So the rhythm itself has to grow, and it only has to grow for a thumb. Under a
coarse pointer the row is 44px; under a fine one it stays 29px, which is above
the 24px WCAG 2.5.8 minimum and is what keeps five columns scannable. The footer
pays 105px per grid row of columns for it, which is the whole cost: 461px to
566px at 1440, 756px to 951px at 768, and 908px to 1164px at 375.

Verified by measuring every target under both pointer types at 375, 768 and 1440:
minimum 44 by 44 everywhere, and zero overlapping pairs, which is the property
the naive version would have failed. The fine pointer footer is byte identical at
768 and 1440 and 16px taller at 375, from screenshot hashes taken before and
after against the same build.

Separately, the header's Log in and Sign up buttons carried two heights at once.
`sizes.xxs` said `h-8` and both call sites passed `className="h-9 px-[18px]"`,
and `cn` is a plain join with no tailwind-merge, so the element kept both and
rendered at 36px only because Tailwind emits height utilities in ascending order
and the later one won. It produced the right answer for the wrong reason: the
same override written to make a button shorter would have been emitted first,
lost silently, and read in the diff exactly as though it worked. `sizes.xxs` is
`h-9 px-[18px]` now, which is what it always rendered as, and neither call site
overrides anything. The header bar is byte identical at 375, 1100 and 1440.
