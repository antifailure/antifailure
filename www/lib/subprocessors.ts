/**
 * The subprocessor list, and the log of every change to it.
 *
 * This is data rather than prose in a page component because the list is the
 * part of the legal set that goes stale fastest: a vendor is added the day
 * somebody wires a client, and a security review asks for the list by name. One
 * file, one array, one commit is the shortest path from "we now use X" to "the
 * published list says so".
 *
 * Every entry here was established by reading the code that talks to the vendor,
 * not by recalling what a product like this usually uses. `evidence` records
 * where, so the next person can re-check the row instead of trusting it. A
 * vendor with no reachable client code does not go on this list, and one with
 * reachable client code does not come off it.
 */

/** Whether the vendor receives data on every run, or only under a condition. */
export type Engagement = "always" | "conditional";

/**
 * Which of the two things here engages the vendor.
 *
 * A FIELD RATHER THAN A SENTENCE, because the distinction is the one a security
 * review is actually asking about and prose cannot be relied on to carry it.
 * `product` is the hosted control plane and what runs a customer's checks;
 * `website` is antifailure.dev, which a person reads. PostHog is engaged by the
 * second and by nothing in the first, and the page groups on this so that
 * cannot be lost by somebody inserting a row in the wrong place: without it,
 * PostHog sorted into the conditional group and rendered under the heading
 * "Model providers receive nothing unless you give us a key", which is a
 * sentence about a different thing entirely.
 */
export type Scope = "product" | "website";

export type Subprocessor = {
  /** The contracting party, as a security review expects to read it. */
  name: string;
  /** The specific services used, not the vendor's whole catalogue. */
  service: string;
  /** Why the data goes there. */
  purpose: string;
  /** The categories that actually reach the vendor. */
  data: string;
  /** Where the processing happens. */
  location: string;
  engagement: Engagement;
  /** Whether the product engages this vendor or the marketing site does. */
  scope: Scope;
  /** For a conditional vendor, exactly what turns it on. */
  condition?: string;
  /** The code that proves the row, so it can be re-checked rather than trusted. */
  evidence: string;
};

export const SUBPROCESSORS: Subprocessor[] = [
  {
    name: "Microsoft Corporation",
    scope: "product",
    service:
      "Azure Container Apps, Azure Database for PostgreSQL, Azure Key Vault, Azure Blob Storage, Azure Table Storage, Azure Log Analytics, Azure Static Web Apps",
    purpose:
      "Runs the hosted control plane and this site, and stores everything the control plane holds.",
    data: "Account name and email, GitHub identifiers, session records including IP address and browser user agent, organization and repository metadata, policy, run events, audit entries, and the name, work email, company and message somebody leaves on the contact form.",
    location:
      "United States, Azure Central US. The region is enforced by a validation rule in the infrastructure code, so a deployment to another region fails at plan time rather than moving data quietly.",
    engagement: "always",
    evidence: "infra/terraform/modules/control-plane, infra/terraform/stacks/control-plane",
  },
  {
    name: "GitHub, Inc.",
    scope: "product",
    service: "GitHub OAuth, GitHub Apps, GitHub Container Registry",
    purpose:
      "Signs people in, reads the repository and membership metadata an organization grants, and stores the control plane's container image.",
    data: "GitHub account identifier, login, email, display name, avatar URL, and the installation and repository identifiers for repositories an organization connects. No production data and no run contents.",
    location: "United States",
    engagement: "always",
    evidence: "web/apps/api/src/auth/github.ts, web/apps/api/src/github/app.ts",
  },
  {
    name: "PostHog, Inc.",
    scope: "website",
    service: "PostHog Cloud US: product analytics, autocapture and session replay",
    purpose:
      "Answers where somebody gave up on the marketing website. The first party counter beside it cannot: it sends no address, no element and no ordering, by design, so it can say how many people reached the pricing page and never why they left it.",
    data: "The marketing website only. PostHog, Inc. receives the page address including its query string, the referrer, one page view per route, autocaptured clicks and form submissions carrying the element's tag, classes, ids and visible label text, the browser, operating system, device type and screen size, and a session recording of the pages visited. A recording holds the page structure and styling, cursor movement, clicks and scrolls. EVERY INPUT VALUE IS MASKED IN THE BROWSER BEFORE IT IS SENT, so a recording of the careers form or the enterprise contact form shows fields filling up with asterisks and never the name, work email, company, links or message typed into them. Advertising identifiers are stripped out of the address. NOT YOUR IP ADDRESS: the endpoint in front of PostHog does not forward it, so PostHog never receives it and the geography on a PostHog dashboard describes our datacenter rather than any reader. No cookie is set, and the identifier lives in sessionStorage for one tab, so nothing here joins two visits. No account, organization, repository, policy, run, audit entry or production data reaches PostHog: this row is about the website and not about the product.",
    location:
      "United States, PostHog Cloud US. The browser sends to an endpoint this project runs on its own domain, at app.antifailure.dev, which forwards to PostHog. THAT IS TRANSPORT AND IT IS NOT A BOUNDARY, and the difference is the whole reason this row exists: it changes the destination your browser connects to, not who receives the data. PostHog receives it either way. What it buys is that a content blocker's vendor list does not match the request, so the measurement is not silently half missing, and that your address is dropped on the way through. It is the same site and not the same origin, because this site is a static export with no server of its own to forward anything. Where that endpoint is not reachable the requests fail and are dropped; nothing falls back to sending them to PostHog directly.",
    engagement: "conditional",
    condition:
      "Only for a person browsing the marketing website, and only where that person is being measured. Global Privacy Control, Do Not Track, the switch on the privacy page and a browser that reports itself as automated each stop it, and each of them stops it BEFORE the library is fetched rather than after: a reader who has said no causes no request for PostHog's code at all, so there is no recorder to have read the page. Nothing in the hosted control plane, the engine, the runner or the command line calls PostHog.",
    evidence: "www/lib/posthog.ts, www/components/ProductAnalytics.tsx, www/lib/beacon.ts",
  },
  {
    name: "Anthropic PBC",
    scope: "product",
    service: "The Claude API",
    purpose:
      "Model-driven planning: deciding the next action an exploratory user takes, and synthesizing a response for a third-party API that is not reachable from the twin.",
    data: "Whatever the request carries. In the paths this product ships, that is a workflow description, the page address and title, field and control names, and up to 4,000 characters of the twin's visible page text; or one outbound request line and up to 4,000 bytes of its body. Raw HTML, cookies, and local storage are excluded by construction.",
    location: "United States",
    engagement: "conditional",
    condition:
      "Only when an organization stores an Anthropic key with the control plane and routes model calls through it. With no key the engine plans deterministically and sends nothing.",
    evidence: "web/apps/api/src/providers/proxy.ts, runner/src/model.ts, engine/cmd/af-proxy/synth.go",
  },
  {
    name: "OpenAI",
    scope: "product",
    service: "The OpenAI chat completions API",
    purpose: "The same model-driven planning, when an organization chooses OpenAI instead.",
    data: "The same categories as the Anthropic entry above.",
    location: "United States",
    engagement: "conditional",
    condition:
      "Only when an organization stores an OpenAI key with the control plane and routes model calls through it.",
    evidence: "web/apps/api/src/providers/proxy.ts, runner/src/model.ts, engine/cmd/af-proxy/synth.go",
  },
];

/**
 * Vendors a reviewer will ask about that are not on the list above.
 *
 * Naming them is cheaper than answering the question four times, and it is the
 * half of a subprocessor page that is usually missing: a list of who you use
 * says nothing about whether you looked for the rest.
 *
 * TWO OF THESE USED TO CLAIM THE STRONGER OF TWO DIFFERENT THINGS, and the
 * distinction is the whole reason this comment exists. "This deployment sends
 * nothing to Stripe" and "this software cannot send anything to Stripe" are
 * different promises. The first is about configuration and the second is about
 * code, and the entries for payment and for email made the second one while the
 * repository contained a real Stripe client and a real Resend mailer, each one
 * environment variable away from active.
 *
 * The rule this page now follows: say what is true of the CODE, which anybody
 * can check by reading it, and describe the configuration as a named condition
 * rather than asserting a state of the deployment. A statement about which
 * variables are set on a server is one no reader can verify and one that stops
 * being true the day somebody sets them, which is precisely how these two came
 * to be wrong.
 */
export const NOT_ENGAGED: [string, string][] = [
  [
    "Payment processors, and the condition on that",
    "No card details reach any system here, and none can: checkout and the billing portal are pages Stripe hosts, so a card is entered on Stripe's own form and this product never sees one. That part is unconditional. What is conditional is the rest: the control plane contains a real Stripe client, and it is active when AF_STRIPE_SECRET_KEY and AF_STRIPE_WEBHOOK_SECRET are set. Where they are, Stripe processes an organization's plan, subscription and invoice records and is a subprocessor for that deployment. Where they are not, the billing routes refuse and name the missing variables, and nothing reaches Stripe at all. The control plane says which of the two it is on the first line it logs at startup. This entry used to say there was no billing and that the only Stripe code was an offline simulator, which was true when it was written and stopped being true when the billing work landed.",
  ],
  [
    "Email and messaging providers, and the condition on that",
    "Two different things share this heading and only one of them is conditional. A customer application's own outbound message, to Resend, SendGrid, Postmark, Amazon SES, Twilio or Slack, is intercepted by the side-effect firewall, recorded locally and never delivered, and that is unconditional. Separately, the control plane itself can send one kind of mail, a sign-in link, through Resend, and that path is active when AF_RESEND_API_KEY, AF_MAIL_FROM and a public URL are all set. Where they are, Resend receives the address the link is sent to and is a subprocessor for that deployment; setting some of the three and not all of them stops the process at startup rather than half enabling it. This entry used to say nothing in the product could send a message, which described the firewall correctly and the control plane's own mail not at all.",
  ],
  [
    "Error tracking, and the rest of the analytics question",
    "PostHog IS engaged now, for product analytics and session replay on the marketing website, and it has its own row on the list above rather than a softened sentence here. A third party does see something, and no sentence on this page is allowed to imply otherwise: the requests travel through an endpoint we run, and PostHog, Inc. receives what they carry. THIS ENTRY USED TO SAY THERE WAS NO POSTHOG. That was true when it was written and stopped being true the day the marketing site started sending, which is the same failure the payment and email entries above record about themselves. What is still true is everything the row above is careful to exclude: PostHog is a vendor for the WEBSITE, and no account, organization, repository, policy, run, audit entry or piece of production data reaches it. There is no Sentry, no Datadog, no Bugsnag, no Google Analytics, no Mixpanel, no Amplitude and no Plausible in anything this repository wrote, and there is no crash reporter in the engine, the runner, the command line or the control plane, none of which calls any analytics vendor at all. The first party counter described on the privacy page still runs beside PostHog and still sends what it always sent: a channel and a page shape from closed lists, and a random identifier that lives in sessionStorage for one browsing session. The control plane exposes metrics for an operator to scrape and exports nothing. TWO SCRIPTS ARE FETCHED AT RUNTIME, and this entry used to say the site loads none from another origin: PostHog's session replay recorder, which comes from the endpoint this project runs at app.antifailure.dev rather than from any vendor host, and the cal.com booking widget on the contact page, which loads app.cal.com/embed/embed.js and an iframe behind it when a reader scrolls near it. The first is our own infrastructure serving a third party's code; the second is a third party's. That iframe reports its own errors to a Sentry host, which is cal.com's document doing cal.com's error tracking on cal.com's origin rather than anything here, and it is named because a reader's browser opens the connection either way.",
  ],
  [
    "Other model providers",
    "Only Anthropic and OpenAI are accepted. Azure OpenAI, Google, Amazon Bedrock, and local model servers are refused by the provider validation rather than silently supported.",
  ],
  [
    "Other clouds",
    "The hosted control plane runs only on Azure. There is no AWS, Google Cloud, Vercel, Cloudflare, or Fastly in its path.",
  ],
];

/** When the list above was last checked against the code. */
export const SUBPROCESSORS_REVIEWED = "5 September 2026";

/**
 * Every change to the list, newest first.
 *
 * A subprocessor page with no history is a page a customer has to diff by hand
 * against a screenshot they took last quarter. Adding a row here is part of
 * adding a row above, not a separate courtesy.
 */
export const SUBPROCESSOR_CHANGES: { date: string; change: string }[] = [
  {
    date: "5 September 2026",
    change:
      "PostHog, Inc. added, for product analytics and session replay on the marketing website only. Named rather than quietly added: the entry below the list said in as many words that there was no PostHog, and a company that sells boundary discipline does not get to soften that by deleting the sentence. Every input value is masked before it leaves the browser, no cookie is set, and a reader who has asked not to be tracked never fetches the library at all.",
  },
  {
    date: "30 August 2026",
    change:
      "First published. Microsoft and GitHub listed as engaged for every organization; Anthropic and OpenAI listed as engaged only for an organization that stores a model provider key.",
  },
];
