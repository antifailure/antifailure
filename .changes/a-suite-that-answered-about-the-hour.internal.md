# fixed

`deploy/status/render_test.sh` gave a different answer depending on the hour it
ran at, and on the morning of 2026-09-03 the answer was five failures against a
renderer nobody had touched. Every open pull request that changed anything
under `deploy/status/` inherited that red, so the gate was reporting on the
clock rather than on the branch.

Nothing had regressed. The suite built its fixtures from the wall clock and
then asserted things about a moment, and the moment moved. It moved in two
separate ways.

The hour. "Three checks today, one of them failed" was three readings at two
hours, one hour and ten minutes before now. That is one UTC day at noon and two
UTC days at half past midnight. The case did not fail when the day split under
it, it quietly became a different case: two days holding one and two checks,
neither of them partly failed, so no bar was drawn as partial and no strip
label said one of three. The same split runs through the outage case, where a
pass and a fail an hour apart have to land on one day, and through the rounding
case, whose four hours of readings have to sit inside one day.

The date. The incident fixtures carry real dates and the page shows a rolling
fourteen days of incident history. The closed incident is dated 2026-08-20. It
left the window on 2026-09-03 and took three assertions with it, and the
2026-09-20 maintenance window would have stopped being called scheduled on
2026-09-21.

So the clock is a fixture now. `render.sh` reads `STATUS_NOW_EPOCH` when it is
set and the system clock when it is not, which is what the workflow does and
what the published page keeps doing. It reads the clock exactly once and hands
the number to `page.jq` and `feed.jq`, neither of which ever asks for itself.
The suite pins it at 2026-09-01T12:00:00Z.

Measured rather than argued, under a `date` that lies about now and tells the
truth about everything else. The suite as it stood returned between zero and
ten failures across ten clocks, a different set of failures at almost every
one. The suite as it stands returns zero across twenty-seven clocks, spanning a
year back, every three hours of a day, and five years forward.

The pin is not passing vacuously. Remove the injection from `render.sh` so it
reads the wall clock again and six assertions go red at once. Mutate `page.jq`
five times, one mutation per originally failing assertion, each removing
exactly the capability that assertion names, and every one of the five says no.
