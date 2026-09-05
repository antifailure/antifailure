# fixed

The careers form on the deployed site said "Could not reach the server" and
nothing anybody had could see it. The site publishes on every merge to main and
the control plane only moves when a version tag is promoted, so the page was
live against an API twenty two commits behind it, and every check was green: the
tree declared the route, the form posted to it, and the agents that drive the
product on every pull request were pointed at a disposable stack built from one
commit, where the front end and the API necessarily agree.

`tools/sitesmoke` drives the real deployed site with the product's own agent and
asserts on what a PERSON sees. It fills the careers form in on every hostname
the site answers on, presses the button, and requires the page to show the
control plane's own answer. When it does not, the failure quotes the sentence
the page actually showed rather than saying that something went wrong. It runs
on a schedule as well as after a deploy, because the failure that is live today
arrived with no deploy at all: a second custom domain was bound to the site and
the control plane had never heard of it, so every form on `www.antifailure.dev`
has been refused ever since. It files no job applications: the scheduled
workflow answers the optional work link with a URL the control plane's own
validation refuses, so the request reaches the handler and is turned away before
anything is written.

The agent needed four fixes before it could have found this at any target. A
checkbox reports a value whether or not it is ticked, so the snapshot called
every required acknowledgment and every radio group already answered and the
planner skipped them. A required field whose label matched no known shape was
left empty, so the browser refused to submit the form at all. The submit button
said "Send application", which is on no list of words that move a workflow
forward, and once the document's own submit controls were consulted the site
header's "Sign in" link still won, so the agent filled in the whole form and
then navigated away from it. And "Could not reach the server" was on no list of
failure signals, so the page telling the agent that its request never arrived
was judged unreadable, the verdict was unverified, and unverified exits zero.

An expectation may now be quoted, and a quoted one is required on the page
character for character. Without that, expectations are judged by how many of
their meaningful words appear anywhere on the page, which is right for a
sentence about a product and wrong for a sentence a page either renders or does
not: the control plane's own refusal scores six of its seven words against the
careers page before the form has been touched.
