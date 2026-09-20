# added

A manifest can ask for a desktop run.

The desktop driver reached the runner and stopped there. Nothing in a manifest
could name a desktop application or a desktop workflow, so the engine sent the
runner a document with none in it on every run that has ever happened. A driver
nobody can reach is not a feature, which is the same defect the terminal list
was added to close, one surface over.

Two new keys. `desktop` says which application the workflows drive: its kind,
electron or macos, and the path to it. `desktop_workflows` is what the agents
do in it, written exactly as a browser workflow is, as a goal and what proves it
happened. They run in the same `af test` against the same environment, are
selected by the same `--only`, share one namespace with the other two lists
because a name is what a report prints, and are counted and reported exactly
like a browser result.

A run whose only workflows are desktop ones is a run: the count that decides
whether a manifest declares any work now includes all three lists, and a
manifest that declares desktop workflows with no application, or an application
no workflow drives, is refused with which of the two it is.
