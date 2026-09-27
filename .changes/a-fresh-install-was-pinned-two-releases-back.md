# changed

A fresh `helm install` with no image tag, and a fresh Terraform apply with no
`image_tag`, selected v1.5.5 while v1.7.0 was the release being served. Two
releases skipped the step that moves those defaults, so the gap was two tags
rather than one and anybody who installed the chart without pinning a tag got
software two releases older than the release they had just read the notes for.

The three defaults now name v1.7.0: the control plane stack's `image_tag`, the
module's `image_tag`, and the chart's `appVersion`, which is what `image.tag`
falls back to. The chart's own `version` is unchanged, because it moves when the
chart changes and the two numbers only agree by coincidence.

Nothing already running changes. A deployment made from the tag's own image is
already on v1.7.0, and an installation that pins its own tag is unaffected. What
changes is what a fresh install reaches for when it is not told.
