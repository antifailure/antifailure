# fixed

The enterprise licensing page told a customer to install a license with a
command that stores nothing.

Its "Installing a license" section was three commands, `af license install
<token>`, `af license status`, `af license remove`, and neither binary installs
anything. On the enterprise binary `af license install` prints "This binary
reads its license key from AF_LICENSE_KEY and stores nothing" and returns. On
the community binary it refuses. There is no override in `ee/`: the enterprise
entry point reads `AF_LICENSE_KEY` and `AF_ORG` from the environment and that
is the whole mechanism.

Neither variable appeared on any published page. So the command told the reader
to set two variables, pointed them at
`https://antifailure.dev/docs/enterprise/licensing`, and that page could not
answer the question the message raised. A closed loop, on the paid path.

The page now says there is nothing to install, shows the two variables, and
says why storing a key on disk was rejected. It also documents
`AF_LICENSE_PUBLIC_KEYS` for an installation that mints its own licences, and
that those keys are merged with the build's rather than replacing them, because
trusting your own key must not stop the vendor's from working.

Separately, `af doctor` tells somebody with a busy port range to "set
AF_PORT_RANGE_START to a range that is free", and that variable was documented
nowhere either. It is in the local runtime guide's Ports section now, beside
the error it follows.

Both were found the same way, by diffing every AF_ variable named in a
user-facing string against every AF_ variable any published page mentions. The
first version of that diff was line oriented and returned a clean zero over
`AF_PORT_RANGE_START` while looking straight at it, because `r.Remediation =
fmt.Sprintf(` and the string that names the variable are on different lines. It
reports correctly over a window, and it was watched failing by taking the new
mention back out.
