# added

A feature can be turned off for everybody, or for one customer, without a deploy.

Feature flags, targeted by user, organization, project, repository, plan or
environment, with a percentage rollout and a kill switch. Checkout and every
administrative money write are behind one.

Two decisions worth knowing about. A DENY target beats an ON flag, because
taking one customer out of a feature that is working for everybody else is the
common incident and turning the whole flag off punishes the rest of them. And
killing a flag is recorded as a different event from turning it off: the same
change, completely different reasons, and the one worth finding six months later
is the incident, so a kill demands a reason and stamps who and when.

A kill switch and a rollout default in opposite directions, deliberately. An
unknown flag is OFF for a rollout, so a mistyped key leaves an unreleased
feature unreleased. An unknown flag is NOT KILLED for a kill switch, because a
control plane that has never had that row would otherwise refuse every checkout
on every self-hosted installation, none of which has any flag rows at all.

The operator screen says whether anything actually READS each flag. Finding out
that a switch has no call site by flipping it during an incident and watching
nothing happen is the worst possible moment to learn it.
