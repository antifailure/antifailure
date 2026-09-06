# fixed

Five destructive buttons in the console acted on the first click.

Cancelling a subscription raised the browser's own unstyled popup, and
tearing down an environment, removing a runtime, removing a provider key and
revoking a terminal's credential asked nothing at all: a misclick on Revoke
killed the credential, and a driver steering the console from outside a
browser either hung on the native popup or sailed through it. Every other
destructive action in the console already used the shared confirmation
dialog, which names the thing and says what happens to it.

All five now open that dialog. Cancelling a subscription asks for the plan's
name to be typed and says when the paid period ends and what the organization
drops to. The other four say what stops, what keeps running and whether the
thing can come back, and the write is not sent until the red button is
pressed.

The operator portal's index and its data tables also asked the browser for
every page they linked to as soon as the link scrolled into view, the same
storm the navigation rail had already been cured of. They now fetch a page
when the pointer or the keyboard reaches its link, which is what the rail does.

An invitation's expiry and a deletion export's dates showed a bare calendar
day with no time and no timezone, on the two pages where the exact time is
the whole point. They now carry the exact local time like every other date
in the console.
