# added

A manifest can now ask for a mobile run.

The iOS driver was written, driven end to end against a real simulator, and
unreachable. Nothing in a manifest could name it, so the engine sent a job
document that never said `ios` and the driver only ever ran from a test. That
is the same defect the terminal surface had one release earlier, and it is
worth naming plainly: a capability nobody can ask for is not a capability, and
`available: true` on a surface no manifest can reach is the same false promise
as a flag over a dead code path.

Two keys, following the pattern `terminal_workflows` established rather than
inventing a third. `mobile` names the application under test and the device it
runs on. `mobile_workflows` are the goals, written exactly the way a browser
workflow is written: a sentence and what proves it happened.

The application is NOT part of each workflow, and that is the one design
decision here worth reading. It is a property of the environment a run
rehearses, the same way `base_url` is: one run installs one application on one
device and drives it. Repeating the identifier and the artifact path on every
workflow would be one value written many times, and the first time two of them
disagreed the run would have to either reinstall between workflows or quietly
honour one and ignore the rest.

iOS and Android share one list with a required `platform`, where terminal got a
list of its own. The two decisions look alike and are not. A browser workflow
and a terminal workflow share the sentence and nothing else, so one entry would
have half its keys refused by whichever surface it was not. iOS and Android
need the SAME keys and differ only in how the values are spelled, so splitting
them would duplicate every field to encode a single discriminator.

A manifest declaring both browser workflows and mobile workflows is refused
rather than half run. The surface tells the runner what to open, and a mobile
run opens a device and no browser, so a manifest carrying both lists would run
its mobile workflows and SILENTLY NOT RUN its browser ones, reporting a verdict
that covered half of what it declared while looking complete. Refusing it says
so at the line that is wrong.

Two more refusals, both of the same kind: a field that would be silently
ignored is refused instead of ignored. An iOS run naming an Android activity or
an Android emulator image has written down an intention nothing will act on. An
iOS run whose artifact is an `.apk`, or an Android run whose artifact is a
`.app`, fails deep inside the device tooling with a message about a bundle
rather than about the line of the manifest that was wrong.
