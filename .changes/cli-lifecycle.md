# added

`af update`, which installs the latest release rather than telling you how to.

Upgrading meant going back to the website and running the install script by
hand, and the CLI knew that and said nothing. A command that prints another
command is not an upgrade path; it is a note asking somebody to go and find
one, and the version they land on is whatever the page happens to serve.

`af update` reads the published release, downloads the archive for this
platform, checks it against the SHA256 in `checksums.txt`, and replaces the
binary and the runner source that shipped with it. What it will not do is as
deliberate as what it will. It never writes to a shell profile, to PATH, or to
anything inside a project. It refuses a package managed installation and an
enterprise binary rather than guessing, because the upgrade for those belongs
to the package manager and the enterprise distribution. It refuses to
downgrade. `af update --check` reports the latest release and changes no file.

The order it works in is the part that matters when something goes wrong. The
archive is verified before anything is unpacked, unpacked into a staging
directory beside the binary before anything is moved, and the binary is
replaced last, by a rename, so a failure at any earlier step leaves the
installation that was already working. A failure after the runner source has
moved restores it, and if that restore fails too, the original is kept and its
path is printed instead of being deleted. An interrupted update is recorded and
finished on the next run, and the lock means two of them cannot race.

# fixed

Doctor now reports whether this binary is current and whether the project's
manifest is valid.

It checked what the machine could do, and answered "This machine can run
Antifailure" to somebody holding an obsolete binary in a directory whose
manifest did not parse. Both were true statements about a machine and neither
was true about the installation.

The manifest check uses the same discovery and validation the lifecycle
commands use, so it cannot disagree with them: an invalid manifest fails, and
an absent one is reported as the ordinary state of a directory that has not
been initialized yet. Neither path writes a project file. The version check
reads the published release with a three second deadline and fails for an
outdated stable release.

What it will not do is claim to know. A development build, a version newer than
anything published, and a lookup that could not complete are each reported as
unknown, because a version check that cannot reach the network and answers
"current" is worse than no check. A run that finishes with warnings no longer
signs off with "This machine can run Antifailure" either, which is what it did
while carrying a check that had just said it could not tell.
