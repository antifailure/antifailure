# fixed

A GitHub delivery that arrived out of order could restore access somebody had
taken away. Every webhook handler wrote the installation row through one
function that cleared `suspended_at` unconditionally, so a `repository` or
`installation_repositories` delivery retried after a suspend or an uninstall put
the installation back to live, and sign-in grants membership on exactly
`suspended_at IS NULL`. Only the `installation` event, which is the one that
means the App is installed right now, clears it.

`af ci` said nothing when teardown failed. A teardown that could not reach the
daemon printed no line at all, which on a green run reads as `--keep`, and one
that ran and left containers behind printed only how many it removed. It now
names each resource that stayed and what refused it, the way `af down` always
has, and says to run `af down` where the job ran. The exit code is unchanged:
it is the verdict on the change under test, and G10 is what gates a leak.

Model spend that crossed a month boundary was charged to nothing. A budget row
is keyed on the month, and the two halves of one call read the month
separately, so a completion that started at 23:59 on the last day of a month was
checked against that month's cap and charged to the next one, which has no row
until somebody sets a cap. The UPDATE matched nothing and the charge vanished
without an error. The spend is charged to the month that authorised it, and a
charge with no row to land in is now an error rather than a silent zero.

Refusing a device login that had already been approved answered "declined" and
declined nothing. The console now shows what happened and points at where to
revoke the token the terminal already holds.
