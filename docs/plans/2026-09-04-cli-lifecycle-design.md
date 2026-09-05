# A doctor result that describes this installation

The user hit an obsolete binary and an invalid manifest while doctor reported
that the machine could run Antifailure. The current command checks machine
dependencies, not either condition. Login already exists and is not duplicated.

Doctor will include two checks. The manifest check uses the same discovery and
validation functions as the lifecycle commands. An invalid manifest fails; an
absent manifest warns with initialization instructions. Neither path writes a
project file. The version check reads GitHub's public latest release endpoint
with a three second deadline, compares numeric stable versions, and fails for
an older release. Development builds, an unpublished newer version and lookup
failures remain explicitly unknown rather than being counted as current.

For upgrades, the current simplicity mandate supersedes PROGRESS section 10's
minimum of printing another command. The updater reuses the release archive naming
and published SHA256 contract without downloading or executing a shell script.
It stages and verifies the binary and runner source before changing either, moves
the old source aside, and atomically renames the binary as the final step. Failed
binary replacement restores the old source; if restoration itself fails, the
original source is preserved at a named recovery path. An installation lock stops
concurrent updates. The check option changes nothing. Package-managed and unknown
installation layouts are refused rather than guessed at.

Tests cover version ordering, a current release, unpublished and development
versions, failed and malformed HTTP responses, offline uncertainty, missing and
invalid manifests, a valid manifest, verified archive installation, checksum and
download failures, incomplete archives, path traversal, links, and rollback.
Each assertion must be independently mutation-tested. The command reference is
regenerated from the real command tree. No live installation is modified during
verification.
