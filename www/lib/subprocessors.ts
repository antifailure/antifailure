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
  /** For a conditional vendor, exactly what turns it on. */
  condition?: string;
  /** The code that proves the row, so it can be re-checked rather than trusted. */
  evidence: string;
};

export const SUBPROCESSORS: Subprocessor[] = [
  {
    name: "Microsoft Corporation",
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
    service: "GitHub OAuth, GitHub Apps, GitHub Container Registry",
    purpose:
      "Signs people in, reads the repository and membership metadata an organization grants, and stores the control plane's container image.",
    data: "GitHub account identifier, login, email, display name, avatar URL, and the installation and repository identifiers for repositories an organization connects. No production data and no run contents.",
    location: "United States",
    engagement: "always",
    evidence: "web/apps/api/src/auth/github.ts, web/apps/api/src/github/app.ts",
  },
  {
    name: "Anthropic PBC",
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
    "Analytics and error tracking",
    "No third party sees anything. There is no Sentry, no Datadog, no PostHog, no Google Analytics, and this site loads no script from another origin. What it does do is count page views itself: a channel from a closed list, a page shape from a closed list, and a random identifier that lives in sessionStorage for one browsing session and cannot join two visits. The referrer and the URL are turned into those bounded values in your browser and never sent. There is no cookie, and the counter turns itself off if you have set Global Privacy Control or Do Not Track. The control plane exposes metrics for an operator to scrape and exports nothing.",
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
export const SUBPROCESSORS_REVIEWED = "30 August 2026";

/**
 * Every change to the list, newest first.
 *
 * A subprocessor page with no history is a page a customer has to diff by hand
 * against a screenshot they took last quarter. Adding a row here is part of
 * adding a row above, not a separate courtesy.
 */
export const SUBPROCESSOR_CHANGES: { date: string; change: string }[] = [
  {
    date: "30 August 2026",
    change:
      "First published. Microsoft and GitHub listed as engaged for every organization; Anthropic and OpenAI listed as engaged only for an organization that stores a model provider key.",
  },
];
