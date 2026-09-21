# fixed

A desktop run that names a macOS application already running is refused rather
than attached to.

`open -a` activates an application that is already open instead of launching a
fresh one, so a run drove whatever the last run, or the person at the keyboard,
had left on screen. The verdict was then about a state nobody in that run
created: a workflow could pass because an earlier one left the form filled in,
and it did, in front of us, with its own step list showing it never filled the
field it was judged on.

Refused, never terminated. An application path can name Slack, Mail or your own
editor, and nothing here will quit one of those to make a run possible. The
refusal names the application, says this run did not start it, says why that
makes the verdict worthless, and gives the two ways forward: quit it, or leave
the application path out, which is how a run attaches to a running copy on
purpose.

Both kinds of desktop application now make the same promise by different
means. An Electron run has always launched its own process; a macOS run now
refuses rather than inherits. A run's verdict depends only on what that run
did.
