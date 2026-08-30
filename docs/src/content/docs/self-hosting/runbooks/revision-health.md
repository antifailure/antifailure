---
title: Revision health
description: A replica is restarting in a loop, or fewer replicas are running than were asked for.
sidebar:
  order: 12
---

Two alerts share this page because they usually fire together and always have
the same first question.

**`restart-loop`, severity 1.** One replica restarted more than three times in
fifteen minutes.

**`replicas-below-minimum`, severity 2.** At some point in fifteen minutes,
fewer than two replicas were running.

## The distinction that matters

A restart is not a failure. The liveness probe restarts a container that stops
answering `/health`, which is the probe doing its job, and one restart during a
deploy is normal. Three in a quarter of an hour is a container that cannot
start.

Production runs two replicas so that this is not an outage. That is the whole
reason for the second replica, and it is also why the second alert can say
anything: on a single replica deployment, "fewer replicas than configured" and
"the service is down" are the same event, and the availability probe says it
louder.

## What to check

```sh
az containerapp revision list -n afcpprod-app -g af-cp-prod-centralus \
  --query "[?properties.active].{rev:name,replicas:properties.replicas,health:properties.healthState}" -o table
az containerapp logs show -n afcpprod-app -g af-cp-prod-centralus --tail 200
```

The application refuses to start rather than degrade, on purpose, in three
cases. Each writes the reason to the log before exiting:

- A half configured GitHub App. The id, the private key and the webhook secret
  are all three or none, because an App that verifies deliveries perfectly and
  cannot act on them is worse than no App.
- A provider key sealing secret that is not exactly 32 bytes.
- A database URL that does not parse.

None of those is fixed by restarting. All three are fixed in Key Vault or in
Terraform, and the container will keep looping until they are.

**If the log shows nothing at all**, the image is wrong. A digest that does not
exist, or a registry the managed identity cannot pull from, produces a replica
that never runs a line of the application.

## The trap that has caught this stack three times

An apply that touches the container app template creates a **new revision at
zero percent traffic** and reports success. Terraform owns the template,
continuous deployment owns the traffic. So a revision can be restart looping
while every customer is served perfectly by the old one, and the reverse is also
possible.

Always read which revision has the traffic before deciding what is broken:

```sh
az containerapp ingress traffic show -n afcpprod-app -g af-cp-prod-centralus -o table
```

## What not to do

**Do not scale up to make the alert stop.** More replicas of a container that
cannot start is more restarts.

**Do not delete the revision.** It is the evidence, and in `Multiple` mode it
is costing nothing while it holds no traffic.
