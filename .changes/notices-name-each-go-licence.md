# changed

`THIRD_PARTY_NOTICES.md` now names the licence of every Go module the binary
links, as an SPDX expression read from the files the module itself ships, and
reproduces the NOTICE files those modules distribute. It used to list each
module's path and version and nothing else, which told somebody reviewing the
binary's licensing nothing, and left out the NOTICE files the Apache License 2.0
asks to be carried with a redistribution.

The licence is recognised, not guessed. The generator knows the licence texts
the linked modules actually carry, names every licence it finds in a file
rather than the first, and fails naming the module and the file when it cannot
identify one, so a gap in the notices stops the build instead of shipping.
