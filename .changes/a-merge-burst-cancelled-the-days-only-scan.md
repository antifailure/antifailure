# security

A burst of merges to main cancelled the day's scheduled vulnerability scan
while it was still pending, so the daily scan never completed. The watchdog
that exists to notice a scan has stopped running reported it, correctly, and
main went red for a reason no code change caused.

Two halves. The scheduled scan now has its own concurrency group, so activity
on main cannot reach it. And the watchdog skips a cancelled run rather than
reading it as a failure, because GitHub uses that one word for three unrelated
things and none of them is a verdict. The freshness limit still decides, so a
schedule that is cancelled every day ages out and alarms anyway, and a scan
that genuinely fails still alarms at once.
