# added

A real native macOS application the desktop surface is proven against, and the
first run of `desktop.kind: macos` that has ever happened.

`electron` had a fixture and an end to end test. `macos` had a schema enum
member, a branch in the document builder and a driver, and nothing had ever
watched it drive an application. That is the same dead shippable gap as a
function with no call sites, one enum member wide, and it sat behind a value a
manifest was already allowed to write.

`runner/test/fixtures/ledger-native` is an AppKit application built by swiftc
into a bundle. Two things in it are load bearing and look like detail. The
acknowledgment is `setAccessibilityRequired(true)`, because AppKit publishes no
such thing on its own and the planner ticks a checkbox only when it is
required, so without that line the form can never be submitted and the failure
reads as a driver that cannot press things. And it carries no placeholder,
because with one the field's `filled` read true in two of four snapshots of a
freshly launched window while the reader's own tree carried no value either
time, and a planner skips a field it believes is answered.

The test drives it from a real manifest through the engine's own document
builder and the real runner, and it proves the one thing only this kind can:
the manifest writes no `process`, so the name the runner finds the application
by is the one normalisation derived from the bundle. Until a native application
was really launched, that derivation had never had a live subject and could
have produced any string at all with every test still passing.
