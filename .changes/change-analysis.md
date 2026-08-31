# added

`af change` reads the diff of a pull request and says which checks will
exercise what it touched. Every changed path is classified by a rule that names
it, every check is reported as selected or not alongside whether the manifest
configures it at all, and the report says what reading a diff cannot see. It
opens no environment and touches no database, so it is the one thing the
product can say before it has spent anything.

The rule that decides how much to trust it: a path no rule recognises selects
every check rather than none, and so does a diff too large to classify and a
diff with no files in it. The two mistakes are not symmetric. A path wrongly
called documentation skips work nobody finds out about; a path wrongly called
unknown costs a run that shows up in the report.

It never grades the change. There is no score and no risk word in the output,
because both would be a judgement made from a file listing, and a tool that
calls a change safe is making a promise the terms of this product refuse to
make. What it produces is one shape of sentence: this file is X, and X is
exercised by check Y, and here is the rule that decided.

An added line naming an outbound host is checked against the egress policy
using the same code that decides real traffic, so a pull request that starts
calling something the manifest does not mention says so before the run rather
than after it.

`change.rules` in the manifest teaches the classifier a layout the built in
rules do not predict. A rule says what a path is and never which checks to run,
it cannot claim a surface the manifest already derives, and a pattern matching
every path is refused, because one would classify everything and the fail safe
above would never fire again.

# changed

The example GitHub Actions workflow runs `af change` first and gates `af ci` on
its output, so a pull request that touches nothing any check exercises no
longer gets an environment. It checks out with `fetch-depth: 0`, because a
one commit deep clone shares no history with the base branch and there is no
merge base to diff against.
