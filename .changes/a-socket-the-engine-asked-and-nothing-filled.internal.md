# fixed

`tools/socketcheck` now asks whether a binary a customer runs puts anything
into each extension socket, not only whether the engine reads it.

It reported every socket consulted while the emulator socket was empty in every
build anybody could run. `engine/internal/env/emulator.go` asks the registry for
an emulator, which is what the gate counted. `emulator.RegisterBuiltin` is the
one function that fills that registry, and nothing shipped called it, so a
manifest naming any of the AWS, Azure or GCP emulators this repository declares
was refused with "this build has no emulators registered at all" while the gate
was green and accurate.

Each socket is now also either registered by a shipped binary or listed in the
tool as deliberately empty, with the reason. Registered means a call to the
socket's `Add` method that the binary can reach, found by walking from `main`,
every `init` and every package level initializer through the repository's own
packages, for the files the release's build matrix compiles. A call in a
package no shipped binary imports does not count, and neither does one in a
function nothing calls or one only a test makes. Every main package in the
repository is named as shipped or not shipped, so a new binary cannot change the
answer without somebody deciding whether it ships.

Three sockets are empty on purpose and listed. The golden store and the
datastore provider have built in switches that run before the registry is asked,
and nothing in the repository implements the lifecycle hook. The emulator is
not listed, so the gate fails on it until something shipped registers the
emulators. An exemption with a blank reason, one naming no socket, and one whose
socket a shipped binary has since started filling are each refused, and the
older list of sockets the engine does not consult is held to the first two rules
as well.
