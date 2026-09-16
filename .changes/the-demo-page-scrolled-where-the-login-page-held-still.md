# fixed

The request-a-demo page scrolled where the login page it copied held still.

Both pages wear the same split shell, a cover on the left and a column on the
right, but the demo column spent its height like a page with two fields when it
carries eight. A tall intro, a card padded at p-7 with a gap-y-5 grid, a
one-line marketing consent stretched to two, and a two-paragraph footer stacked
past the fold, so on the laptop heights people here actually use, 800 through
982 tall, the submit button and the sign-in link sat below the fold and the
page scrolled. The login page next to it sits inside the viewport, and the ask
was that this one match it.

It now does, without dropping a field the sales team asks for. The intro is
tighter, the two-column grid is denser inside a compact card, the consent and
the privacy line are terse, and the footer is a single line that keeps the
sign-in-with-GitHub link and the quickstart note. Measured over CDP with device
emulation at 1280x800, 1440x900 and 1512x982, the document height equals the
viewport height at each, so there is no page scroll; the honeycomb cover, the
restraint and the field set are unchanged. On a phone, where eight fields
cannot share the room two do, it stays a clean single column with no horizontal
overflow, the same as the login page does.
