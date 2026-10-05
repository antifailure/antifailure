# fixed

`AF_BIN_DIR` put `af` where you asked and the runner where `af` never looks.
`af` finds the runner it shipped with relative to itself, beside its own
directory, and the installer placed it under `AF_PREFIX` whatever `AF_BIN_DIR`
said, so an install into `~/.local/bin` reported success and `af runner
install` then answered AF-AGT-004. The runner now goes to
`share/antifailure/runner` beside the bin directory, which for the default
layout is the same place as before.

An upgrade that failed part way no longer leaves a new `af` with no runner. The
installer replaced `af` first and then deleted the old runner before copying
the new one, so a copy that failed, or a runner directory that could not be
moved because `af test` was running from it, left the machine with neither.
The new runner is now staged beside the old one, and each is put back if
anything after it fails. Both installers, install.sh and install.ps1.
