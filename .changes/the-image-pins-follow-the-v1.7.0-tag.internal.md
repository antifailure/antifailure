# changed

The control plane module and stack default `image_tag` and the chart's
`appVersion` named v1.5.5 while v1.7.0 is the release being served. Two releases
skipped this step, so the three pins were behind by two tags rather than one.

Internal: nothing a user of the product can observe changes. These are the
defaults a fresh apply or a fresh `helm install` reaches for.
