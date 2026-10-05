#!/usr/bin/env bash
# Installs Chromium and its system libraries for Playwright, with a wall clock
# bound on every attempt, and says whose fault a failure was.
#
# Run it from the directory whose node_modules holds the Playwright version
# that will drive the browser, exactly where `npx playwright install` ran.
#
# Why it exists: on 2026-10-05 the `engine` job on two main commits, 34d2b2880
# and 1b0a7e59d, was cancelled at its 45 minute timeout with nothing judged,
# so cd deployed neither. The step that ate the job was this install, which
# normally takes 14 to 39 seconds and took 35 and 36 minutes, because the apt
# mirror it fetches fonts from was trickling at about 9 KB/s. A cancelled job
# is the worst shape of red: it reads like concurrency, it judges nothing, and
# it hides every step after the hang.
#
# Each attempt gets AF_INSTALL_ATTEMPT_SECONDS (default 300). Measured across
# main's fourteen CI runs before this change, the 27 healthy installs took 14
# to 39 seconds and the one stalled install took 2200, so 300 is about eight
# times the slowest healthy one and a seventh of the stall.
# and there are AF_INSTALL_ATTEMPTS of them (default 3). A fresh attempt opens
# fresh connections, which is what gets a different mirror backend. When every
# attempt runs out of time the step fails RED and names the network, so the
# reader knows this red judged nothing about the code. When an attempt fails
# fast instead, that is not a stall, and the message says so and gives the exit
# code, because calling every failure a network failure would teach the next
# person to re-run a real defect.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
"$here/apt-network.sh"

attempt_seconds=${AF_INSTALL_ATTEMPT_SECONDS:-300}
attempts=${AF_INSTALL_ATTEMPTS:-3}
timed_out=0
last=0

for ((i = 1; i <= attempts; i++)); do
  echo "::group::playwright install, attempt $i of $attempts, bounded at ${attempt_seconds}s"
  started=$SECONDS
  # The apt processes that exist before this attempt, so that only this
  # attempt's own are stopped below.
  before=" $(pgrep -d ' ' -x 'apt-get|dpkg' || true) "
  # `--kill-after` because a process that ignores the polite signal must not
  # stretch the bound; a bound that waits for the bounded thing to agree is not
  # a bound.
  if timeout --kill-after=30 "$attempt_seconds" npx playwright install --with-deps chromium; then
    echo "::endgroup::"
    echo "installed on attempt $i in $((SECONDS - started))s"
    exit 0
  else
    last=$?
  fi
  echo "::endgroup::"
  if [ "$last" -eq 124 ] || [ "$last" -eq 137 ]; then
    timed_out=$((timed_out + 1))
    echo "attempt $i ran out of its ${attempt_seconds}s"
  else
    echo "attempt $i failed with exit code $last after $((SECONDS - started))s"
  fi
  # timeout stops npx, but Playwright installs the libraries by running
  # apt-get as root through sudo, and that apt-get outlives it: measured on a
  # runner, attempt 1's `apt-get update` was still running after its bound,
  # holding the dpkg lock, so attempt 2 failed in two seconds on the lock and
  # the retry bought nothing. So this attempt's own apt-get and dpkg are
  # stopped, and only those: anything that was running before it is left.
  stray=""
  for pid in $(pgrep -x 'apt-get|dpkg' || true); do
    case "$before" in *" $pid "*) ;; *) stray="$stray $pid" ;; esac
  done
  if [ -n "$stray" ]; then
    echo "stopping what attempt $i left running:$stray"
    # shellcheck disable=SC2086
    sudo kill -TERM $stray 2>/dev/null || true
    for _ in $(seq 1 30); do
      # shellcheck disable=SC2086
      sudo kill -0 $stray 2>/dev/null || break
      sleep 1
    done
    # shellcheck disable=SC2086
    sudo kill -KILL $stray 2>/dev/null || true
  fi
  # An attempt stopped inside dpkg leaves packages unpacked but unconfigured,
  # and the next apt-get refuses until they are configured. Stopped while
  # downloading, this has nothing to do.
  sudo dpkg --configure -a || true
done

if [ "$timed_out" -eq "$attempts" ]; then
  echo "::error title=Network, not code::Every one of $attempts attempts to install the browser ran out of its ${attempt_seconds}s. Healthy installs take 14 to 39s, so this is the package mirror or the browser download stalling. Nothing after this step was judged; this red says nothing about the change."
else
  echo "::error title=Browser install failed::$attempts attempts, $timed_out of them out of time; the last failed with exit code $last. A fast failure is not a stall: read the attempt output above before re-running."
fi
exit 1
