---
title: The control plane is unreachable
description: The availability test failed from two locations. What that rules out, and what to check in order.
sidebar:
  order: 9
---

**Alert:** `unreachable`. **Severity 0.** The service is down for customers.

An availability test asked `https://app.antifailure.dev/readyz` from three
Microsoft managed locations and at least two of them failed inside fifteen
minutes. Each agent retries a failed request before reporting it, so this is
already not a single dropped packet, and two separate locations agree.

## What it has already ruled out

The probe asks for the customer's name over TLS, so it exercises DNS, the
custom domain binding, the certificate, the ingress and the application. Any one
of those is enough to fire it. That breadth is the point and it is also why the
first job is to narrow it.

`/readyz` is not `/health`. It takes a connection out of the pool the
application serves with and asks the database a question, and it answers 503
when the database does not. A 503 here is the application telling the truth.

## Thirty seconds, in this order

```sh
curl -sS -o /dev/null -w '%{http_code} %{ssl_verify_result}\n' \
  https://app.antifailure.dev/readyz
curl -sS https://app.antifailure.dev/readyz
dig +short app.antifailure.dev
```

**No DNS answer.** The CNAME is gone or the zone is broken. It lives in the
`af-web` resource group, not in the control plane's, so a change there is the
first thing to look at.

**A TLS error.** Go to [the certificate](/docs/self-hosting/runbooks/certificate/).

**503 with a reason.** The database. Go to [the database is not
answering](/docs/self-hosting/runbooks/database-unreachable/).

**404 or an Azure error page.** The custom domain binding, or traffic is on a
revision that is not serving. Check what is actually serving:

```sh
az containerapp ingress traffic show -n afcpprod-app -g af-cp-prod-centralus -o table
az containerapp revision list -n afcpprod-app -g af-cp-prod-centralus \
  --query "[?properties.active].{rev:name,healthy:properties.healthState}" -o table
```

**Nothing answers at all.** Ask the generated address, which skips DNS, the
binding and the certificate in one step:

```sh
az containerapp show -n afcpprod-app -g af-cp-prod-centralus \
  --query properties.configuration.ingress.fqdn -o tsv
```

If that address is healthy and the custom name is not, the fault is in the four
resources in `infra/terraform/modules/control-plane/domain.tf` and nowhere else.

## What not to do

**Do not roll back before reading what is serving.** In `Multiple` revision
mode the previous revision is still running at zero percent. Moving traffic
back to it is one command and a few seconds. Redeploying is minutes, during
which the broken revision is still taking requests.

**Do not assume a deploy caused it** without checking. This alert fires for a
certificate, a DNS record and a database, none of which a deploy touches.

**Environments are not down.** Customers running `af up` in their own
continuous integration are unaffected, and their engines buffer events to disk
until this comes back. The
[operations page](/docs/self-hosting/operations/) explains what that recovery
looks like, and it needs nothing from you.
