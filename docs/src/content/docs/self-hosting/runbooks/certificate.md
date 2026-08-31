---
title: The certificate
description: The certificate on the custom domain has fewer than three weeks left, or the check could not complete.
sidebar:
  order: 18
---

**Alert:** `certificate-expiring`. **Severity 3.** Working hours.

A separate availability test, running every fifteen minutes from one location,
fails when the certificate presented on `https://app.antifailure.dev` has fewer
than 21 days of life left.

## Why a probe rather than a metric

Azure emits no metric for the remaining life of a Container Apps managed
certificate. A probe that is told to fail below a threshold asks the same
question from the other end, and it has the advantage of checking what is
actually being served rather than what Azure believes it issued.

It is a separate test from the availability one on purpose. Putting the SSL
check on that test would make a certificate with nineteen days left page
somebody at three in the morning as an outage.

## The first thing to rule out

**This alert also fires when the test could not complete at all.** If the
service is down, this fires alongside `unreachable`. Deal with
[unreachable](/docs/self-hosting/runbooks/availability/) first and come back;
this one is about the certificate only when the site is otherwise fine.

## What to check

```sh
echo | openssl s_client -servername app.antifailure.dev \
  -connect app.antifailure.dev:443 2>/dev/null \
  | openssl x509 -noout -subject -issuer -dates

az containerapp env certificate list -n afcpprod-env -g af-cp-prod-centralus -o table
az containerapp show -n afcpprod-app -g af-cp-prod-centralus \
  --query properties.configuration.ingress.customDomains -o json
```

## Why a self renewing certificate did not renew

Azure renews a managed certificate on its own, so this firing means the renewal
did not happen. Renewal revalidates domain control, and validation reads DNS. So
the cause is almost always DNS rather than certificates:

- The `CNAME` for the name no longer points at the container app's generated
  address.
- The `TXT` record at `asuid.app` is gone or holds the wrong verification id. A
  CNAME alone proves that a name points at an Azure endpoint, not that it points
  at **this** endpoint, which is why Azure wants both.

Both records are owned by Terraform in
`infra/terraform/modules/control-plane/domain.tf`, and both live in the
`antifailure.dev` zone in the `af-web` resource group rather than in the control
plane's own group. A plan will show a difference if either has been changed by
hand.

```sh
dig +short app.antifailure.dev
dig +short TXT asuid.app.antifailure.dev
az containerapp show -n afcpprod-app -g af-cp-prod-centralus \
  --query properties.customDomainVerificationId -o tsv
```

## What not to do

**Do not delete the custom domain binding to force a reissue.** That takes the
site off its own name, and the certificate cannot be issued while the name does
not resolve to the app, so the recovery is longer than the problem.

**Do not buy a certificate.** Three weeks is enough time to fix a DNS record.
