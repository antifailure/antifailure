# changed

The homepage's Isolated Twin section draws the twin again, instead of a mock
console with a card sitting on top of the numbers it was explaining.

The figure that shipped on 2026-08-31 was a schematic: the twin's DNS, app,
workers, state and credentials wired together on the left, production dimmed
and dashed on the right, a red cut between them reading "no route", and along
the bottom of the twin the three seals the run actually proves, lighting green
one at a time as the beats go by. It said what the section's heading says.

Two rewrites later the same slot held a "Deployment safety score" panel: a
gauge, four metric rows, and a floating "Release blocked" card pinned to the
bottom right of the panel. That card is the reason this is a defect rather than
a preference. It sat over the metric rows, so at the end of the film, which is
the state a reader arrives to, the "Checkout p99" reading was hidden behind it,
the "Safe state restored" label was hidden behind it, and the "Lock duration"
value it was there to explain was hidden behind it. The one number left visible
was the one the card was not about. On a phone it was worse: the card covered
the panel outright and the heading behind it was faded to a fifth of its
opacity, which reads as a rendering fault rather than a transition.

The schematic is back, and the one thing wrong with it is fixed rather than
carried along. It is a 940 unit sheet, and a 320 pixel phone gives this figure
about 220 pixels of width, so it was drawn at under a quarter size and every
label in it landed near two pixels tall: not a small drawing, an illegible one,
and it shipped that way. PR #188 had already set the rule for the solutions
pages, that a wide figure is redrawn below the small breakpoint rather than
scaled down. Below 1024 pixels this figure now draws its own narrow version,
with the rows and the three seals as real text at 12 and 13 pixels, the deny cut
as a rule across the middle, and production listed under it with its state and
its keys struck through. The header carried the same fault: one row held the
label, four beats and the remaining count, and on a phone the two readings that
say what the run is doing were pushed off both edges. It is two rows below 1024
and one above.

Both drawings read one list of seals and one list of rows, because they drew the
same three seals from two separate literals before, and two copies of one fact
is how the last figure in this file came to disagree with itself.

Nothing here animates on a loop. The film runs once when the section is scrolled
into view, settles, and stops, and it does not run at all under
`prefers-reduced-motion`, which is given the finished state directly.

`gray-new-50` was measured in this repository at 3.85:1 and is not used for any
text in the new drawing; `gray-new-40` is 5.9:1 on white and carries the row
kickers and the secondary readings. The destroyed count reads `#1f7a00` at
5.5:1 rather than the brand green, which is 2.4:1 and unreadable as text.
