# fixed

On Windows a migration rehearsal reported fast statements as taking no time.

Go's clock on Windows advances in steps of the system timer, measured at 0.3 to
0.5 ms, so a statement quicker than one step, which is most DDL on a small
table, was timed at 0 ms and looked exactly like a statement that was never
timed. Statement durations on Windows now come from the performance counter and
are accurate to the microsecond, as they already were on Linux and macOS.
