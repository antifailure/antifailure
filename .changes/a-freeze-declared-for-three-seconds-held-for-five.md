# fixed

A database freeze declared to hold for 3 seconds held for 5.

The durability proof stopped its writers before it thawed the database, and
gives each writer two seconds to finish the statement it is on. Against a
frozen database no statement finishes, so every freeze lasted its hold plus
those two seconds. The time in place that `af chaos` now reports showed it,
5.003s against a declared 3s, and the daemon agreed: the container was paused
for between 4.9 and 5.3 seconds.

The proof now thaws the database at its declared hold and stops the writers
after, and the same freeze measures 3.001s. The durability claim is unchanged
and covers more: commits the writers make after the thaw are counted, and each
one the client was told was committed must still be there afterwards, like
every earlier one.
