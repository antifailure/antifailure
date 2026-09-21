# fixed

A SQL workload counted its own deadline as failed statements, so a run with
nothing wrong printed errors against a query that had never failed.

A transaction interrupted by the run ending has never been counted as a failed
transaction, because the caller stopping is not the database failing. The
statements inside it were counted anyway. A ten second run against a real
database committed 5416 transactions, reported nothing failed and nothing
retried, and still listed five errors beside a read that had just run 3640
times and returned a row every time: the five clients that happened to be
inside a statement when the duration expired. The summary line above the table
said nothing was wrong, so the table was the only thing a reader had, and it
was the part that was wrong.

A statement stopped by the run ending is no longer counted as a failure, which
is the decision the transaction around it already made. A statement that the
database really refused is counted exactly as before.
