# fixed

The Plan page no longer refuses an owner because its shared browser session
still holds the role they had before a promotion.

The production ordering was exact. On 2026-09-03 an active session began at
04:50:45 UTC. The membership changed to owner at 12:22:59 UTC. The shared layout
correctly preserved its already ready session state across navigation. When the
Plan page opened, `billing.get`, `subscriptions.current` and
`subscriptions.invoices` each read the current owner role from the database and
succeeded. The page then replaced those results with a refusal based on the old
client role.

The Plan page now leaves access to those three server procedures. The shared
session refreshes after a successful role change, GitHub membership sync or
member removal, and when the window regains focus or a hidden tab becomes
visible. Only the newest response wins when those refreshes overlap. The
session response cannot be stored in a browser or intermediary cache. If a
refresh fails, the live shell keeps the last good answer and says that its
controls may be out of date, with a retry beside it.

The mobile shell also returns keyboard focus to the menu button after its
drawer closes, rather than leaving focus on a control that no longer exists.

The operator analytics dashboard is also absent from customer navigation. Its
page and navigation now share one check that requires both the operator
organization marker and the `analytics.read` permission. Owners and admins of
the operator organization can open it. Members, viewers, customer owners and
installations with no operator configured cannot. The session reports only the
boolean marker, never another organization's slug.
