# fixed

`inject_declared_faults` added the database outages of separate faults together
and reported the total as one number.

The faults run one at a time and each is undone before the next begins, so their
outages are separate events. Adding them produces a figure that is
arithmetically true and describes an outage that never happened: 4000 reads as
one four second gap when it was two gaps of two seconds, and those are different
facts about a system. It is the mirror of a zero in a field nobody measured,
which the same tool is careful to avoid when a durability proof did not run.

It reports the longest single outage now, named for that, with the per fault
numbers in the detail. A caller that wants a total can add them; a caller handed
a total cannot recover the parts. The commit counts beside it are still summed,
because a commit lost under either fault is a commit lost.

Found in review rather than by a test, and extending it turned up three more of
the same shape: the multi fault fixture gave values to only one fault, so
summing and taking the last value were indistinguishable for phantom rows, in
flight commits and the summary's own aggregate.
