# changed

The dispatch workflow template calls `af workload run` instead of assembling
flags in a shell case statement. The `command` input now names a workload kind
rather than a verb, so the console, the workflow and the engine share one
vocabulary; `agents` and `load` still resolve, so a copy of the file taken
before this keeps working. It gained `seed`, `concurrency` and `run_id` inputs,
and it uploads the result document as an artifact.

An input the case statement had no flag for was dropped without a word. It is
now refused by name.
