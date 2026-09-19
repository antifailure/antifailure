# added

The Start a run card dispatched agents and load, and that was all it could say.

The workflow template every customer runs already declares a seed and a
concurrency input, and the engine already reads them: a seed makes two runs
decide the same way so a before and an after can be compared, and a concurrency
ceiling caps requests in flight so a load run stays inside a limit. The console
had no field for either, and the dispatch verbs did not forward them even when
something set them, so the one place a customer drives runs from could neither
reproduce a run nor hold a load down. The card now carries the knobs: a seed on
an agents run, and seconds, scale, concurrency and a seed on a load run.

Each knob is optional and a blank one is left out of the dispatch rather than
sent as a zero, so it reaches the command's own default. That is not only tidy.
A seed or a concurrency the customer never set would reach a workflow file that
may predate those inputs, which GitHub answers with a 422 that fails the whole
run, so an unset knob must not appear in the request at all.
