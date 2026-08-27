---
title: Azure
description: Running the hosted pieces on Azure, and what to do about quota.
sidebar:
  order: 2
---

Nothing here is required. The engine runs on a laptop and in a GitHub Actions
runner with no cloud account. This is for running the control plane and a
shared environment pool yourself.

## Quota

```
AF-INF-001 The cloud API returned a quota error for standardDSv5Family in
eastus.
  Next: Request more standardDSv5Family in eastus, then run the command again.
```

The first thing to check on a new subscription, before anything else, because
the default limits are low and a quota increase can take a day to be approved.

```sh
az vm list-usage --location eastus -o table
az quota list --scope /subscriptions/<id>/providers/Microsoft.Compute/locations/eastus
```

Ask for the family the node pool uses, not the total. A subscription can have
plenty of total cores and none of the family a pool wants, and the error names
which.

## Naming and tagging

Everything created is in a resource group prefixed `af-` and tagged
`project=antifailure`. That is what makes it safe to clean up: a delete scoped
to that tag cannot reach anything else in the subscription.

## What runs where

| Piece | Service |
| --- | --- |
| Control plane | Container Apps or AKS |
| Its Postgres | Azure Database for PostgreSQL, flexible server |
| Environment pool | AKS, one namespace per environment |
| Golden storage | Blob storage |
| Secrets | Key Vault |

## The state of this

The Kubernetes runtime is not built. `runtime.provider: kubernetes` is named in
the manifest schema and refused at startup with a message saying so, rather than
quietly giving you containers on whichever machine ran `af`.

So today this page covers running the control plane on Azure, which works, and
the environment pool, which does not exist yet. Said plainly here because a
self hosting page that described an architecture nobody can run would be worse
than a short one.

Related: [the control plane](/docs/self-hosting/control-plane/),
[configuration](/docs/reference/control-plane/).
