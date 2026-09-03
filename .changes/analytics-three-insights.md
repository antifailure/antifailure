# added

Weekly and monthly distinct counts, a conversion funnel over events with a
window, and retention as a cohort grid. All three were impossible before, and
for one reason: the rollup grouped by a subject and then threw it away, so
nothing could follow one organization across two days or one session across two
events. The rollup now keeps a working set the application has no grant on, and
publishes counts computed from it.
