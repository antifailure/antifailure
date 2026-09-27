# fixed

Two failures printed a raw tool error at the reader instead of the sentence the
installer composes to explain what happened, and in both cases the message meant
for that reader was sitting right there underneath it.

A refused download printed `curl: (56) The requested URL returned error: 404`, so
the one line install answered a mistyped `AF_VERSION`, a platform a release does
not carry, and a rate limited archive with exactly the kind of noise the message
below it was written to replace. `curl` is invoked with `-sS`, which is silent but
shows errors, and of the three places `install.sh` fetches something only this one
failed to discard the tool's own stderr. The number in that line is curl's error
code rather than the HTTP status, and the same 404 surfaces as 22 or as 56
depending on how the request failed, so a reader who takes it for a status reads a
different number every time and neither one is the status the next line reports
from the same URL. BusyBox wget, which is the wget Alpine ships, ignores its quiet
flag for its error line and wrote `wget: server returned error` there too.

An install target that could not be created printed `mkdir: /some/path: Permission
denied` straight after `Checksum verified`, with none of the installer's own words,
and printed it twice whenever the binary directory sat under the prefix, which is
the default. The message four lines below it covers a different case: it fires when
the binary cannot be written into a directory that already exists, and says nothing
about a directory that could not be made. The two directories are created
separately now, each reporting its own path and naming `AF_PREFIX` and
`AF_BIN_DIR` as the way through, so an unwritable binary directory no longer leaves
an empty prefix behind on the way out either.

`AF_BIN_DIR` and `AF_PREFIX` are documented, which they were not. `AF_PREFIX` moves
the whole installation and `AF_BIN_DIR` moves only the binary, which is what to
reach for when `af` should land in a directory that is already on the PATH.

The tests could not see either leak, and for one reason. Every check for a raw
error lived in the helper that runs a SUCCESSFUL install, so it never ran on the
paths that produce one, and the failure path tests each assert that the sentence
they care about is present and the sentence it replaced is absent, neither of which
can see a line added beside them. The check now runs on every invocation of the
installer and covers both the fetchers' voices and the shell's.
