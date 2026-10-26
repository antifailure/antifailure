# fixed

A waiter for a branch lock on Windows gave up on a lock that was only part way
through being let go. The wait for a remove still in progress was a hundred
retries a millisecond apart, so on a machine whose sleeps are exact it lasted
a tenth of a second, and anything holding the file open a little longer, an
antivirus scan or an older `af` reading the lock, made the waiter fail with
"the lock changed hands 100 times" instead of taking it. The wait is now two
seconds by the clock, and a lock still refusing after that is reported with
what the last look saw.
