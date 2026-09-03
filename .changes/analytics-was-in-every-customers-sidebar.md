# fixed

The operator's analytics dashboard was in every customer's sidebar, and an
owner could be told their role could not see the plan.

Two defects with one shape: a screen decided what to show from a question it
had not finished asking.

**Analytics.** The page counts arrivals across the WHOLE installation, and its
own docstring calls it the operator's dashboard. The console gated it on
`analytics.read`, which owners and admins of every organization hold, because
it describes a kind of reading rather than a right over the installation. So it
appeared in every tenant's navigation, and clicking it produced a refusal
written for somebody else. On an installation that had never set
`AF_ANALYTICS_OPERATOR_ORG`, which is every installation by default, it refused
the operator too, so the entry led nowhere for anybody.

The session now reports whether this organization operates the installation,
and the entry appears only then. A boolean rather than the slug, because
naming the operator to a tenant is a fact about somebody else.

**The plan page, and two others.** `may()` answers "does this role hold this
permission", and a role that has not loaded is not a role that holds nothing.
Three pages called it before the session resolved and rendered a REFUSAL, so an
owner opening the plan page was told their role could not see it, about a
permission owners are the only holders of. A refusal is the worst thing to show
while waiting, because it is the one message somebody acts on rather than waits
through: they go looking for whoever can change their role.

`permissionVerdict` returns four answers instead of two, and separates a
session that failed to load from a role that was refused, because those send
the reader to different places.
