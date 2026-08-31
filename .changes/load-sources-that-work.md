# changed

`load.source` no longer offers Datadog or New Relic. They were in the schema, so
you could set them, and refused when a run reached them, so they could never
work. Anything unrecognised is now refused by name with the sources that do
work, at validation time as well as at run time. The arrival rate for an access
log is also counted from the log's own timestamps rather than assumed, and when
no line carries a readable timestamp the run says the rate was assumed instead
of presenting a guess as production's number.
