# added

The security check suite had no shared contract to land on, so each family
would have reinvented the verdict, the exit code and the data boundary, and the
four spellings would have drifted.

The suite fans out into families that each rehearse a change against the
sanitized twin: broken access control, injection, a leaked canary, an
unexpected side effect, row level security left off, a weakened response
header, a dependency worth a second look. Written in parallel with no shared
seam, each would have had to decide for itself how a finding becomes a verdict,
which exit code it carries, how it renders in the pull request comment, and how
it stays inside the data boundary the control plane depends on. Those four
decisions made seven times are four contracts that agree until somebody edits
one, which is the drift this repository keeps finding in itself.

This is the spine they share, and it carries no family logic of its own. A
security finding is an ordinary report finding in a new policy namespace, so it
inherits the verdict, the exit code, the comment and the control plane boundary
for free. The change router gains an auth surface for who-may-do-what and emits
the routed units a family and a browser personality run against, so a check
runs at exactly the endpoint or boundary a diff touched rather than across the
whole application. The manifest gains a policy.security block keyed by the
finding rule, so a project decides what each finding does the same way it
decides everything else. A read_security_findings tool projects those findings
for a coding agent, carrying a rule, a level, a location and a bounded
description and never the offending body, response or row. And the gate turns a
security finding into a dynamic security exit code, a verification failure when
a family proved a hole and a policy denial when it refused a change on policy
grounds.

The registry ships empty and wired: with no family registered, the plan gains
no security check and the report is exactly what it was, which is what lets each
family land afterwards by registering against a contract that already holds.
