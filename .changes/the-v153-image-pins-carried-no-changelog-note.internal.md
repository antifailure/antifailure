# changed

The control plane module and stack default `image_tag` and the chart's
`appVersion` moved from v1.5.2 to v1.5.3 once the tag existed, so the
maintenance container app job and a fresh helm install name the release
production already serves. That change carries no user facing behaviour: the
running control plane was deployed from the tag's own image, and these are the
defaults a fresh apply reaches for rather than anything a customer sees.

It is recorded here because the bump landed without a declaration. It belonged
in the pin commit as a `Changelog-None` trailer, the squash that merged it
carried none, and `changecheck` is a job inside `ci.yml`, so the merge left
main red and cd would not deploy the commit. The pins were already correct, so
the remedy is this note rather than a change to them: an internal fragment is
kept and never published, which is exactly what an image pin that follows a tag
warrants, and it returns main to green so the next tag's gate has a verdict to
read.
