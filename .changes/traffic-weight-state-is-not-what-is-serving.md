# fixed

The self-hosting guide explained the zero percent revision trap with the wrong
mechanism. It said the new revision comes up at zero percent "because Terraform
does not touch traffic weights", and Terraform does touch them.

Ignoring an attribute is not omitting it. Terraform sends a traffic block either
way, and `ignore_changes` decides which one: the value refreshed from Azure
rather than the one in the configuration. The configuration asks for
`latest_revision = true` at one hundred percent, which would put every new
revision straight into service. Azure, after any deploy has run, holds a pin
naming one revision. So the apply puts back the arrangement it just read, and
the revision it is creating is not in that arrangement.

That distinction is the difference between a rule and a coincidence. A reader
who believes traffic is never sent has no reason to look at what decides the
outcome.

The other half was documented nowhere. The stored state file keeps the OLD
revision suffix indefinitely, because `ignore_changes` is exactly what stops
anything writing the real weight back. Both environments were stale that way,
each by more than one deploy, and that is the intended resting state rather than
drift to repair. What it costs is worth knowing, and it turns on a distinction
the guide now draws: a plan and an apply REFRESH, so they act on a current
value, while `terraform state show` and `state pull` read the stored file and do
not. On this deployment the stored file named a revision that had already been
deactivated while the plan's own view named the one actually serving. So the
answer to "what is serving" comes from Azure, and an empty plan is not an answer
either, because the attribute that would say so is the ignored one.

Removing that `ignore_changes` is called out in all three places, with the
consequence the stale file misleads people about. The stored suffix is not what
would take effect, the configuration is: `latest_revision = true` wins, traffic
follows the newest revision automatically, every apply puts its own revision
into service with no chance to probe it first, and each apply undoes the pin the
deploy sets. That is a deliberate change to how releases work here, not a
tidy-up of a stale field.
