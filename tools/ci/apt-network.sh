#!/usr/bin/env bash
# Makes every apt client on a CI runner give up on a dead connection and try
# again, then prints the values apt will actually use.
#
# It writes apt's own configuration rather than passing -o flags,
# because the client that stalled is not ours to pass flags to: `npx playwright
# install --with-deps` runs its own apt-get as root, and an -o on our command
# line never reaches it. On 2026-10-05 that apt-get spent 35 minutes of a 45
# minute job fetching fonts from azure.archive.ubuntu.com, with single
# downloads taking 357, 731 and 846 seconds, and the job was cancelled at its
# timeout with nothing judged.
#
# The runner image already ships its own settings, measured on 2026-10-05:
# 80-retries sets Acquire::Retries "1" with a 600 second timeout, and
# zz-retries then sets Retries "1" again with a 15 second timeout. apt lets the
# last file read win, and this script's first two attempts lost that race,
# named 80-af-network and then zz-af-network; the dump check below caught both.
# So this does not join the race. apt reads every file in apt.conf.d FIRST and
# the main file /etc/apt/apt.conf LAST, so a setting there wins whatever the
# image names its parts. The timeout stays at the image's effective 15 seconds
# rather than being loosened; what changes is five retries instead of one.
#
# Be honest about what this buys. Retries and a timeout end a connection that
# has gone SILENT. That night's connections were not silent, they were slow:
# 7.4 MB in 846 seconds is about 9 KB/s, and apt has no minimum speed, so
# neither setting would have fired. The image's 15 second timeout was already
# in force that night and never fired, which is the measurement that says so.
# The bound that ends a slow download is the wall clock around the whole
# install, which install-browser.sh and each step's timeout-minutes provide.
# This file covers the other half.
set -euo pipefail

marker='// antifailure: tools/ci/apt-network.sh'
if ! sudo grep -qxF "$marker" /etc/apt/apt.conf 2>/dev/null; then
  sudo tee -a /etc/apt/apt.conf >/dev/null <<CONF
$marker
Acquire::Retries "5";
Acquire::http::Timeout "15";
Acquire::https::Timeout "15";
CONF
fi

# Printed from apt's own view of its configuration, not from the file, so a
# setting that a later file overrides shows as overridden instead of as
# written. A missing line here fails the step: a config this script cannot see
# applied is a config it must not claim.
dump=$(apt-config dump)
for key in 'Acquire::Retries "5"' 'Acquire::http::Timeout "15"' 'Acquire::https::Timeout "15"'; do
  if ! grep -qxF "$key;" <<<"$dump"; then
    echo "::error title=apt network settings did not apply::apt-config dump does not contain $key; something read after /etc/apt/apt.conf overrides it. Files that set Acquire:"
    grep -rlE 'Acquire::' /etc/apt/apt.conf.d/ || true
    exit 1
  fi
  echo "apt: $key"
done
