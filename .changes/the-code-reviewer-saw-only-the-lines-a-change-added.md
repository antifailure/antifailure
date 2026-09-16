# changed

The static code reviewer read only the lines a change added, so it was blind to
the bug whose cause sits on a line the change did not touch.

A nil dereference on a new line is only a bug because of the line above that left
the value nil. A value used as the wrong shape is only wrong because of where the
shape was set. A new call is only broken because of the contract defined in code
the diff did not add. The reviewer was handed the added lines alone and told to
be sure about nothing it would need the rest of the file to judge, which is
exactly the class of defect a whole file reader catches and a hunk reader cannot.

The reviewer now sends the model each changed file whole, with the added lines
marked and the unchanged lines around them shown as context, and decides on the
whole file while still reporting only on the added lines. A file too large to
send whole falls back to its added lines with a window of surrounding context,
and a change too large for the total budget reviews its highest signal files and
names the rest as unreviewed rather than dropping them in silence. A finding the
model anchors to a context line is dropped, so seeing more of the file never lets
the reviewer report a pre existing bug the change did not introduce.
