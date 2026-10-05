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
# The whole script, every attempt and every recovery between them, fits in
# AF_INSTALL_BUDGET_SECONDS (default 480). Measured across main's fourteen CI
# runs before this change, the 27 healthy installs took 14 to 39 seconds and
# the one stalled install took 2200, so 480 is about twelve times the slowest
# healthy one. It is one total rather than a bound per attempt because three
# of the jobs that call this have 20 minutes in all, and three attempts with
# their own bounds and an unbounded recovery between them added up to more
# than the job had: the job would have been cancelled before this script got
# to say why, which is the failure it exists to prevent.
#
# Each attempt gets at most AF_INSTALL_ATTEMPT_SECONDS (default 300) and never
# more than the budget has left after reserving the time to clean up behind
# it. Recovery is bounded too, so nothing between attempts can run past the
# budget and hide the final message.
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

budget=${AF_INSTALL_BUDGET_SECONDS:-480}
attempt_cap=${AF_INSTALL_ATTEMPT_SECONDS:-300}
attempts=${AF_INSTALL_ATTEMPTS:-3}
# What one round of cleanup can take: 10s for timeout's kill, 10s for this
# attempt's apt processes to stop, and 65s for dpkg to configure what was
# unpacked, with a few seconds over. So the script ends within about
# budget plus a few seconds, and every step that calls it has timeout-minutes
# of 10 against a budget of 8.
reserve=90
# An attempt shorter than this cannot install even on a healthy day, so the
# budget is spent rather than wasted on one.
floor=45

deadline=$((SECONDS + budget))
tried=0
timed_out=0
last=0
notes=""

for ((i = 1; i <= attempts; i++)); do
  left=$((deadline - SECONDS - reserve))
  if [ "$left" -lt "$floor" ]; then
    notes="$notes The budget of ${budget}s had too little left for attempt $i."
    break
  fi
  limit=$attempt_cap
  if [ "$left" -lt "$limit" ]; then limit=$left; fi

  tried=$i
  echo "::group::playwright install, attempt $i of $attempts, bounded at ${limit}s"
  started=$SECONDS
  # The apt processes that exist before this attempt, so that only this
  # attempt's own are stopped below.
  before=" $(pgrep -d ' ' -x 'apt-get|dpkg' || true) "
  # `--kill-after` because a process that ignores the polite signal must not
  # stretch the bound; a bound that waits for the bounded thing to agree is not
  # a bound.
  if timeout --kill-after=10 "$limit" npx playwright install --with-deps chromium; then
    echo "::endgroup::"
    echo "installed on attempt $i in $((SECONDS - started))s"
    exit 0
  else
    last=$?
  fi
  took=$((SECONDS - started))
  echo "::endgroup::"
  # A time-out is decided by the clock, not by the exit code alone. 124 is
  # timeout's own, but 137 is any SIGKILL, and something other than this bound
  # can send one before the bound arrives, the kernel's OOM killer among them.
  # Calling that a stall would send the reader to the network for a failure
  # that was never the network's.
  if [ "$took" -ge "$limit" ] && { [ "$last" -eq 124 ] || [ "$last" -eq 137 ]; }; then
    timed_out=$((timed_out + 1))
    echo "attempt $i ran out of its ${limit}s"
  elif [ "$last" -eq 137 ]; then
    echo "attempt $i was killed (SIGKILL) after ${took}s, before its ${limit}s were up; that is not this bound, so look for memory pressure"
  else
    echo "attempt $i failed with exit code $last after ${took}s"
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
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      # shellcheck disable=SC2086
      sudo kill -0 $stray 2>/dev/null || break
      sleep 1
    done
    # shellcheck disable=SC2086
    sudo kill -KILL $stray 2>/dev/null || true
  fi
  # An attempt stopped inside dpkg leaves packages unpacked but unconfigured,
  # and the next apt-get refuses until they are configured. Bounded, because a
  # recovery that can hang is a recovery that can hide the message below.
  if ! sudo timeout --kill-after=5 60 dpkg --configure -a; then
    notes="$notes dpkg could not finish configuring after attempt $i within 60s."
  fi
done

if [ "$tried" -gt 0 ] && [ "$timed_out" -eq "$tried" ]; then
  echo "::error title=Network, not code::Every one of $tried attempts to install the browser ran out of its time, within a budget of ${budget}s. Healthy installs take 14 to 39s, so this is the package mirror or the browser download stalling. Nothing after this step was judged; this red says nothing about the change.$notes"
else
  echo "::error title=Browser install failed::$tried attempts within a budget of ${budget}s, $timed_out of them out of time; the last exited with code $last. A failure that is not a time-out is not a stall: read the attempt output above before re-running.$notes"
fi
exit 1
