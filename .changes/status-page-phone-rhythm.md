# fixed

Two things on the status page at phone widths.

The component rows had two different heights and nothing about a component
decided which it got. `.comp-h` wraps, so at 390px a row was 24px tall when the
name was short enough to leave the status and the timestamp room beside it and
54px when it was not: five of seven rows one height and two the other, down a
list whose whole job is to be scanned in one pass. Every row is stacked below
640px now, name on the first line and status on the second, so all seven are
48px and the rhythm is a decision rather than a consequence of how long
somebody's component name is. Above 640px every row fits on one line and
nothing changes.

The Day, Week and Month control was 40px tall, which is neither the 24px WCAG
2.5.8 floor nor the 44px this page already gives its subscribe button. It was
the only control on the page between the two. It is 44 now. The help circles
stay at 24 on purpose: they sit on the heading line beside the component name,
and 44 would either push that name off its baseline or reach into the row
above.

Measured at 390px under touch emulation: all seven rows 48px, and every control
either at 44 or at the 24 floor with the reason recorded beside it.
