# fixed

Every loading skeleton in the console was zero pixels wide on a phone.

The stacked table layout put `justify-items: start` on the cell, which makes
its one child shrink to fit, in the grid branch and, because Chromium aligns
block level children the same way, in the unlabelled branch too. A percentage
width inside a shrink to fit box has no basis to resolve against, so it
computes to zero and the box shrinks to that zero. Every bar in `TableSkeleton`
is a percentage, so all 22 of them, on seven pages, rendered as a stack of
empty boxes: 33 bars on /runs, every one 0px wide at 390px, against 54.8 to
168.2px at 1280px where nothing stacks.

The declaration was there to stop a badge stretching to the full width of the
value column. It never did that, because the wrapper that made it unnecessary
landed in the same commit: the badge is inline-flex inside the wrapper, and the
four state badges on /runs measure 51.6, 57.8, 65.9 and 80.8px wide with the
declaration and without it. Removing it leaves 768px and 1280px identical to
the pixel and gives the phone back a skeleton shaped like the rows that replace
it.
