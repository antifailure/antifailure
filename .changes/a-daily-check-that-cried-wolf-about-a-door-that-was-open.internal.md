# fixed

The scheduled Sign-up probe announced THE SIGN-UP PAGE HAS NO WAY IN every
morning for eight days while both ways in were open and working.

Its last step fetched `/signup` with `curl -sSL`. `/signup` has been a 301 to
`/request-demo` since self-serve organization creation was taken off the site,
so the follow landed on the demo page and graded it against an assertion
written for a self-serve sign-up page: a link to the control plane's GitHub
exchange, which the demo page correctly does not carry and never did. The
GitHub exchange moved to `/signin`, where it is still an anchor at
`https://app.antifailure.dev/auth/github`, and the check never followed it
there.

A red mark that is known to be wrong is worse than no check at all. It is read
once, understood to be noise, and then the real one arrives in a mailbox
everybody has learned to skip. This file is the whole reason that gate existed
and it had stopped being able to say anything.

The probe now asserts the promises the site actually makes, each against the
page that makes it and with no redirect it has not asserted. `/signin` answers
200 and carries the GitHub anchor. `/request-demo` answers 200 and renders a
form carrying every field `validateDemoRequest` requires, plus the honeypot
that is the only thing between the demo queue and a bot. The route that form
posts to answers a real CORS preflight from the hostname people type and is
still registered for POST, which is the pair of failures that show somebody
"Could not reach the server" on a page where nothing looks wrong. `/signup`
answers 301 to `/request-demo`, so the URL that was the front door for as long
as the product had self-serve sign-up still lands somewhere true.

It creates nothing. The two requests it sends at `POST /v1/leads` are the inert
ones `www/lib/control-plane-routes.ts` declares: a preflight the OPTIONS
handler answers without reaching the POST path, and an empty body that
`validateLead` refuses before `recordLead`. A probe that filed a demo request
every morning would put a fake lead in front of the person who reads that
queue, which is a worse outcome than a slightly weaker assertion.

Every assertion was run against a case that must fail before it was believed,
including a control serving the good shape and an arm with nothing listening at
all, so a refusal that means "I could not look" cannot be read as a verdict.
