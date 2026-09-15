# fixed

`TestRehearse_TimesEachStatementFromTheServerWhenTheApplierCannot` failed a pull
request that touched none of its code. It reported that `ALTER TABLE orders ADD
COLUMN region text`, a catalogue-only change, took 643.614ms, over a fixed 500ms
ceiling. The ceiling is meant to catch the planted one second sleep charged to the
wrong row. A shared CI server under load crossed it instead.

The two statements beside the sleep are now bounded by the sleeping statement's
own reported duration, not by a fixed number. A slow machine stretches that row as
well, so a neighbour has to be reported as taking as long as a statement that slept
for a full second before the test says the sleep was charged to it. The floor under
the sleep, the check that no two rows share one number, and the check that the rows
add up to less than the run still stand.

One case is no longer caught, and the test says so beside the assertion: a sleep
split between its own row and a neighbour without double counting, where the
sleeping row stays above the floor.
