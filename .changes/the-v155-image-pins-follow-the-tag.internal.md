# changed

The control plane module and stack default `image_tag` and the chart's
`appVersion` moved from v1.5.4 to v1.5.5 now that the tag exists, so the
maintenance container app job and a fresh helm install name the release
production serves. The change carries no user facing behaviour: the running
control plane is deployed from the tag's own image, and these are the defaults
a fresh apply reaches for rather than anything a customer sees.

The bump is a separate commit after the tag rather than part of it, because
`azurerm_container_app_job.maintenance` reads that default with no
`ignore_changes` on its image, so the value is live. Pointing it at a tag the
registry does not carry yet does not produce a stale deployment, it produces a
failed apply on the stack that runs the product. `tools/tagsync` is the gate
that enforces the ordering rather than a comment asking for it.

The declaration is a file rather than a `Changelog-None` trailer on purpose.
The v1.5.3 bump in #500 carried no declaration at all, `changecheck` runs
inside `ci.yml`, and the merge left main red with cd refusing to deploy the
commit until #501 added the note. A trailer would have been the tidier answer
except that the squash which merges a pull request composes its body from the
description and has already dropped a `Changelog-None` trailer once. An
internal fragment is a tracked file, so the squash cannot lose it, and it is
kept and never published, which is what an image pin that follows a tag
warrants.
