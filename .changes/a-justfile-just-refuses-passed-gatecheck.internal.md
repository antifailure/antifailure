# fixed

A justfile that `just` refuses to read passed gatecheck. #409 added a recipe
named `runbookcheck`, 250 lines after the `runbookcheck` #213 added. Git merged
it without a conflict and gatecheck reported clean, because it reads recipes
into a list and compared both copies against CI without ever asking whether a
name appears twice. `just` asks that first and refuses the whole file, so the
conformance job died on `just k8s-conformance`, and on main the same file would
have stopped `just gate` and `just merge`.

gatecheck now refuses a justfile in which one name heads two recipes, and names
every line the name is defined on. Settings, aliases and variables written with
`:=` are not counted, because `just` keeps those apart from recipes. The refusal
lives inside gatecheck, which CI already runs and `just gate` already calls, so
no new step or recipe comes with it.
