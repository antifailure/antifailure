# fixed

A required context went red because a guard asked a different question than the
test.

The egress tests skip when the machine cannot reach the host they decide about,
so a laptop with no network reports a skip rather than a failure that looks like
a broken containment control. That guard made a plain HTTP request. Six of the
tests behind it require HTTPS. HTTP answered, nothing skipped, and the HTTPS
request then failed into `engine`, which gates every merge, on a pull request
that touched no engine file at all.

The guard now takes the origins the test actually depends on, spelled the way
the probe spells them, and the first one is a plain parameter rather than part
of the variadic so a call that guards on nothing does not compile.

A probe that did not get out also used to mean two things at once and say the
more alarming one. The sidecar has always written one decision per request,
allowed or not, so a failed probe now reads that log and reports whether the
sidecar refused a host the policy allows, which is a containment regression, or
allowed it and the connection failed anyway, which is the network. Only one
combination skips, and it needs the sidecar's own record AND a second probe from
the test process to agree the host is unreachable.
