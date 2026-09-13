# added

`af explore` could not be aimed. It ran the goals `antifailure.yaml` declared
exactly as they were declared, and `--only` chose among them without changing
any of them. Asking whether a path works for a viewer rather than the owner,
whether the billing page offers a way onward when you start on it, or whether
the upgrade control exists on a phone meant editing the manifest first and
remembering to put it back. The MCP tool had the same reach: an agent could
select a goal and replay a seed, and nothing else.

`af explore` now takes `--persona`, `--start`, `--viewport`, `--budget` and
`--focus`, and `explore_for_friction` takes `persona`, `start_path`,
`viewport`, `budget` and `focus`. Each overrides the goal for one run and
writes nothing. `phone` is a touch screen and a phone's user agent at 390 by
844 rather than a narrow desktop window, because a layout that switches on
either never reflows for a size alone. A persona the manifest does not declare
is refused with AF-AGT-022 naming the ones it does. A start path that is a URL,
a size outside 320 to 3840 a side, or a budget that is neither a step count nor
a duration is refused with AF-AGT-023 before anything is asked of the
environment.

Every exploration now reports the persona it signed in as, the page it started
on and the window it ran in, both on the terminal and in the JSON, and its
replay line carries the same flags quoted for a shell.

A goal's `budget.duration` now bounds it. The manifest has always normalised it
to ten minutes and refused one that was not a duration, and then sent it
nowhere, so a goal declaring two minutes ran for as long as its steps took. An
exploration now stops at whichever of its steps and its time runs out first,
and a goal that sets no duration stops after ten minutes.
