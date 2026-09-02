# changed

The control plane stack and module now default `image_tag` to `v1.0.0`.

They were pinned to `v0.1.1`, deliberately, until the tag existed.
`tools/tagsync` classifies both as `live` pins, meaning they are read by an
apply from `main` rather than from a released tree, and
`azurerm_container_app_job.maintenance` reads the value with no
`ignore_changes`. So pointing them at a tag before that tag published would not
have produced a stale deployment, it would have produced a failed apply on the
stack that runs the product.

v1.0.0 published at 17:55 UTC, so they can name it now.
