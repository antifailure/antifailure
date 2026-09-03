# fixed

The rehearsal's per statement timing test no longer compares two real
statements to each other.

It ended by asserting that building an index over 50,000 rows takes longer
than adding a nullable column. That is true most of the time and it was never
the property the test exists for. It went red in CI on a pull request whose
diff was one file mode bit and one markdown file: a contended runner spent
84ms adding the column and 33ms building the index. A check that fails for
reasons no diff can explain is worse than one that is missing, because it
teaches everybody to rerun a red job without reading it, and that habit is
what lets a real failure through.

What the test is named for is per statement attribution. When the migration
tool cannot say what it ran, a Rails or Django migration among them, the
server's event triggers say, and each statement gets a duration of its own.
The fixture now plants a statement whose duration is a fact rather than a
measurement, a materialized view over pg_sleep, in the middle of three. The
planted second has to land on the statement that slept and on neither
neighbour, no two durations may be the same number, none may be zero, and they
have to add up to less than the window the applier ran in. Contention can only
make a duration longer, so a busy machine cannot push the sleeping statement
under its floor.

Both of the sleeping statement's neighbours only add a nullable column, which
is a catalogue write and nothing else. That is what makes the ceiling over them
honest. The index build over 50,000 rows went last, because real work has no
upper bound: it was measured at 454.9ms on a loaded machine against the tens of
milliseconds it takes on an idle one, so a ceiling over it would have been the
same defect in a new place.

Between them those assertions catch one total copied onto every row, a
duration charged to the neighbouring statement, a running total that grows
down the list, a single statement reported for a whole file, and a duration
read from the transaction clock rather than the wall clock. Each of the five
was introduced in the engine on purpose, and each turned the test red.
