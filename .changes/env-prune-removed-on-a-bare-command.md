# fixed

`af env prune` removed environments on a bare invocation. Its help said it
"prints what it would do before doing it"; it printed each removal as it
performed it, and the only gate was a default cutoff of a day. Typed with no
flags in an empty directory on a shared machine, it removed nine environments
and eighteen resources belonging to other sessions. Run bare it now lists every
environment on the machine older than the cutoff, says the cutoff, says that
nothing has been removed, and prints the command that removes exactly that
list. Removal needs `--yes`. `--dry-run` still works and means the same as
running bare. Under `-o json` the bare run is a plan document with
`would_remove`, and `removed` stays empty until `--yes`.

`af env reap` and `af golden gc` had the same shape, a removal on the bare
command, and now list first and remove with `--yes` too. A scheduled reap
passes `--yes`. The error remedies that named `af env prune --older-than 0` and
`af golden gc` now name the `--yes` form, and the documentation pages that
described the preview that did not exist describe the one that does.
