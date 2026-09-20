# changed

The control plane module and stack default `image_tag` and the chart's
`appVersion` moved from v1.5.3 to v1.5.4 now that the tag exists, so the
maintenance container app job and a fresh helm install name the release
production already serves. The change carries no user facing behaviour: the
running control plane was deployed from the tag's own image, and these are the
defaults a fresh apply reaches for rather than anything a customer sees.

The declaration is a file rather than a `Changelog-None` trailer on purpose.
The v1.5.3 bump in #500 carried no declaration at all, `changecheck` runs
inside `ci.yml`, and the merge left main red with cd refusing to deploy the
commit until #501 added the note. A trailer would have been the tidier answer
except that the squash which merges a pull request composes its body from the
description and has already dropped a `Changelog-None` trailer once. An
internal fragment is a tracked file, so the squash cannot lose it, and it is
kept and never published, which is what an image pin that follows a tag
warrants.
