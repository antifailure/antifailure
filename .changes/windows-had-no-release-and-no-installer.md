# added

Windows is a platform a release ships to. Every tag now publishes
`antifailure_<version>_windows_amd64.zip` and `antifailure_<version>_windows_arm64.zip`,
each holding `af.exe` and the runner, signed through `checksums.txt` and
described by the bill of materials like the other four. They install from
PowerShell with `irm https://antifailure.dev/install.ps1 | iex`, which keeps
install.sh's promises: the newest release is found without the rate limited
API, a download that does not match its published checksum is never installed,
a refusal says what GitHub actually answered, and the install ends with `af` on
the user PATH and in the terminal that ran it. Reinstalling over an `af.exe`
that an editor holds open as its MCP server works, because the running binary
is moved aside rather than overwritten.

Before this a Windows user had no build, no installer, and an `install.sh` that
told them, from Git Bash, to compile the product from source. That message now
points at install.ps1. `af.exe` is not code signed yet, and the quickstart says
what that means for SmartScreen and Smart App Control.
