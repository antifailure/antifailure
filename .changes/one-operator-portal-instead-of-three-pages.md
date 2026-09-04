# added

The operator portal has an information architecture: six groups and twenty two
sections, with an overview above them, declared in one place so the rail, the
page headings and the permission each section needs cannot drift apart.

Every route exists and opens something from the first commit. A section nobody
has built yet says so, names what will live there and which permission will read
it. It is deliberately not an empty dashboard: a page of zeroes on this portal
is indistinguishable from an answer, and an operator reading "0 failing runs"
off a placeholder during an incident has been lied to by their own tooling.

The overview is a directory rather than a wall of numbers, filtered to what the
signed-in operator's role can actually reach, so somebody who has not used the
portal in a month can find the section they want without opening five of them.

The three screens that already existed keep their behaviour and move to where
the navigation says they are. The operator log gained the half of itself it was
missing: every entry records what changed, the table had no column for it and
dropped it, so the log showed that a plan was changed and never what it was
changed to. That detail is now in a panel beside the entry.

The whole portal works on a phone. The rail becomes a drawer that traps focus,
closes on Escape, returns focus to the button that opened it, and locks the page
behind it, and every list becomes a stacked record rather than a table scrolling
sideways with the two columns you came for out of sight.
