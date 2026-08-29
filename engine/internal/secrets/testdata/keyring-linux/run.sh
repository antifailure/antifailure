#!/bin/sh
# Unlock a real keyring, then run the real tests against it.
#
# The password is a literal here and that is correct: this keyring exists for
# the length of one container and holds nothing but the test's own values. A
# passphrase that guards nothing is not a secret, and generating one would only
# make the script harder to read without protecting anything.
set -e

# Everything below needs a session bus. A container has none, and without one
# secret-tool fails with "Cannot autolaunch D-Bus without X11 $DISPLAY", which
# is the exact string the implementation's unavailable path reports. So the
# whole run is re-executed inside a bus of its own, once.
if [ -z "$DBUS_SESSION_BUS_ADDRESS" ]; then
  exec dbus-run-session -- "$0" "$@"
fi

# --unlock reads the password from standard input and creates the login keyring
# if there is not one, which there is not in a fresh container. --components is
# limited to secrets because the ssh and pkcs11 components want an agent socket
# and a display and would fail for reasons that have nothing to do with this.
# The daemon prints the addresses it is listening on as NAME=value lines, which
# have to reach the environment or secret-tool will start a second daemon of its
# own and talk to the wrong one.
eval "$(printf 'test' | gnome-keyring-daemon --unlock --components=secrets)"
export GNOME_KEYRING_CONTROL SSH_AUTH_SOCK

# A round trip through the command line first, so that a failure in the harness
# is told apart from a failure in the code under test. If this cannot store and
# read a value back, the Go tests would skip and report a pass, which is the
# one outcome worse than a failure.
printf 'harness' | secret-tool store --label='harness' service antifailure-harness account probe
got=$(secret-tool lookup service antifailure-harness account probe)
if [ "$got" != "harness" ]; then
  echo "the harness itself cannot round trip a value through the keyring" >&2
  exit 1
fi
secret-tool clear service antifailure-harness account probe
echo "harness: a real Secret Service is answering"

exec go test "$@"
