# added

Six defect classes, each proved able to make the verdict say no.

Across the 22 most recent merged pull requests here, Antifailure's verdict on
itself read "Every check passed" twenty one times and "Nothing was verified"
twice. The failed column had never held a number. That is consistent with a
product that works and equally consistent with a check that cannot refuse, and
this repository has already shipped the second thing twice: a database
conformance suite that had never turned down a provider, and a coverage gate
that printed a number while skipping the files it could not parse.

So the six things the verdict claims to decide are each broken on purpose now,
one at a time, through the real code and against a real Postgres, a real
browser and a real inventory. A masking rule that misses the column holding
addresses, a row an invariant says cannot exist, a page that shows an error
instead of the confirmation, an ALTER TABLE holding ACCESS EXCLUSIVE for three
seconds, a column the verifier is not allowed to read, and a manifest declaring
a store this build has no golden for. Each case requires the finding by name in
the report a person actually reads, then restores the break and requires the
same case to pass, because a row that can only ever fail is the same defect
wearing the other sign. The suite counts what it proved rather than asserting
it, and on a machine with the Postgres it needs a class that did not run is a
failure rather than a quiet skip.

Two things it found on the way. A migration that held a lock for three seconds
produced four lock findings: one about the customer's table and three about the
rehearsal's own bookkeeping table, its primary key and its sequence, each
telling the author to split a statement that never touched it. Those are
excluded from the sample now. And `af golden refresh` refused a golden whose
only problem was a column nobody could read by printing an empty list of
findings above the sentence "add a rule for each column above", with the error
naming the column discarded on the way out. It names the column, and it no
longer recommends a rule for a problem no rule can fix.
