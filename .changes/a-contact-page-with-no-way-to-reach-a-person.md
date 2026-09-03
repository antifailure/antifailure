# added

A booking calendar on the contact page.

Every route the contact page offered went to a public tracker or to a list. A
private vulnerability report, an issue, a discussion, and a waitlist address
are the right destinations for almost every technical question, and none of
them is a way to speak to somebody about an evaluation or a design
partnership. The domain has no mail exchanger, so there was no address to fall
back to either.

The page now opens with a calendar. It shows real openings, and the times come
from the calendar rather than from a form that promises a reply. The pricing
page's two commercial buttons, on Team and on Growth and Enterprise, now lead
to it: both previously pointed at the waitlist, so the highest value action on
the site collected an email address.

The embed loads only when the section is close to the viewport, so a widget
most visitors scroll past does not cost the page its first paint, and it ships
in one route's bundle rather than the shared one. When the frame does not load,
which is what happens to anyone blocking third party frames, the card carries
the address in full and a button that opens it, because an empty rectangle
where a calendar should be is worse than no calendar at all.
