# fixed

The assistant panel on the home page's Migration Safety card was a drawing of an
application that a keyboard could walk into. Its reply box was tab stop 51 of
119 with no label of any kind, only a placeholder, and no focus ring: it was one
of three controls on the whole page a keyboard user could reach and not see. Its
Send button rendered 9px square, its Skills button 22 by 13, and the three
window-chrome buttons 6.9px square at 768 and never more than 11px at any width,
all of them under the 24px WCAG floor and far under the 44px this project sets
for itself. Typing into the box appended your own words to a fake transcript, so
there was no honest label to give it.

The parts that only depicted an assistant are drawn rather than operated now.
The reply form is gone, with the state and handlers it fed. The window chrome is
three glyphs a screen reader does not see and a pointer cannot press. What stays
interactive is the part that is a real demonstration: the plan toggle that
switches the whole card between the migration as written and the safer path, and
the finding rows that open to show the line the run measured. Those already
carried labels and focus rings. The toggle's target now clears 24px at every
width through an invisible extension rather than by growing the ink, because the
drawing is rendered at 0.6 to 1.0 scale and a 44px control inside it would be a
real button in a picture.

At 320 and 390 the panel floated over a document cropped to its left 43 percent,
so what showed beside it was a column of empty circles, half an avatar and six
lines of text cut mid-word. It read as clipped content rather than as a floating
panel. Below md the card is the report alone, full width, with its own type at
14px instead of 10px, and the report fades at its foot rather than being cut
through a sentence.
