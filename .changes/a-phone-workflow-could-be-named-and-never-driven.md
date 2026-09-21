# fixed

A workflow could say `surface: ios` and never be driven.

The manifest accepted the surface and the engine chose the iOS driver, but
nothing in the manifest could say which application to drive, and the runner
needs the application's identifier to drive anything. So every iOS run was
refused by the runner, after its environment had been built.

A manifest now declares the application in a `mobile` block: its `id`, the
bundle identifier on iOS, and optionally the built `app` to install and the
`device` to use. A workflow that drives a phone with no `mobile` block is
refused when the manifest is read, rather than after an environment has been
paid for, and so is a `mobile` block that no workflow drives. `af explain`
shows the application above the workflows it is driven in.
