# added

An agent driving a project through the MCP server could rehearse, review a
change and read a verdict without leaving the session, and then had to leave it
for the one thing it could not do from there: get onto a newer Antifailure. The
newest engine shipped, the session kept running the old one, and nothing on the
tool surface said so or offered to close the gap.

The server serves an `upgrade` tool now. It installs the latest verified
community release of the CLI and its bundled runner in place, downloading the
build for this platform, verifying its published SHA256 checksum, and swapping
the binary and the runner source atomically; it leaves shell profiles and
project files alone. The check option reports the latest release beside the
installed version and changes nothing. It is the same in-place update `af
update` performs, handed to the server as a function value rather than
reimplemented, so a tool call and the command cannot install different things.

Two honesties come with it. Applying replaces the file this running server was
started from, and the running process keeps its own open copy, so the new
version does not take effect until the server is restarted; the result says so
and reports the versions before and after rather than letting a caller believe
the tools changed under it. And a binary the installer does not own, an
enterprise build, a platform with no release, a checksum mismatch or a failed
download is reported as a refusal that changed nothing, with the reason and the
next step, never as an upgrade that found nothing to do.
