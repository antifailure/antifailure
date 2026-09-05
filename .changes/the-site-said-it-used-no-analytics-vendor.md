# added

PostHog on the marketing site, and three published documents that said there
was none.

The site counts page views with a first party beacon that sends a channel and a
page shape from closed lists and nothing else. That was a deliberate design and
it stays, running beside this. What it cannot do is say where somebody gave up:
it sends no address, no element and no ordering, on purpose, so it can report
that forty people reached the pricing page and never why thirty nine left it.
Autocapture and session replay answer that, and PostHog Cloud US now does it for
antifailure.dev.

The masking is the whole of the change and it is set explicitly rather than
inherited. Every input value is replaced in the browser before anything is sent,
by type as well as by the coarse flag, so a recording of the careers form or the
enterprise contact form shows fields filling up with asterisks and never the
name, work email, company or paragraph typed into them. No cookie is set, and
posthog-js is configured onto sessionStorage rather than its default of a cookie
plus a year of local storage, because two sentences already published say there
is no cookie and that nothing here joins two visits. Requests go to this site's
own origin and are forwarded, so a reader's browser opens no connection to a
posthog.com host.

Global Privacy Control, Do Not Track, the switch on the privacy page and a
browser reporting itself as automated each stop it, and each stops it BEFORE the
library is fetched. The import is dynamic and it is behind the gate, so a reader
who has said no makes no request for PostHog's code and there is no recorder
that read the page and was then told to stop. The switch also reaches PostHog
mid visit: the beacon announces every change to the decision and the recorder
ends with the queue, rather than each new producer being one more line somebody
has to remember at the switch.

`www/lib/subprocessors.ts`, the privacy page and the data boundary page all said
in writing that no third party saw anything and that there was no PostHog. All
three now name PostHog, Inc. as a subprocessor, list what a recording holds and
what is masked, and keep the claim that is unaffected and different: no account,
organization, repository, policy, run, audit entry or piece of a customer's
production data reaches PostHog, because nothing that handles any of those calls
it. The subprocessor page also used to say this site loaded no script from
another origin, which was already untrue: the contact page has been loading
cal.com's booking widget. It says so now.
