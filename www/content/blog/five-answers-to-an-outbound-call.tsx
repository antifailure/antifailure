import type { Post } from "@/lib/blog";

export const EGRESS_MODES: Post = {
  slug: "five-answers-to-an-outbound-call",
  title: "Choose how external services behave in a test",
  dek: "Mock payments, capture messages, or use a sandbox. A policy for each host lets your application run complete flows inside a controlled environment.",
  summary: "Antifailure's seven per-host egress modes and how to choose the right one for an integration.",
  published: "2026-08-27",
  updated: "2026-09-26",
  tags: ["Testing", "Networking", "Third-party APIs"],
  body: (
    <>
      <p>Your application still calls its integrations in a test environment. A signup sends email, a subscription flow calls a payment API, and a background worker posts a webhook.</p>
      <p>Each integration needs an intentional response. Antifailure lets you set a policy per host, so one run can capture email, simulate payments, and block unknown destinations.</p>
      <h2>Seven modes for different jobs</h2>
      <ul>
        <li><strong>BLOCK.</strong> Refuse the request and record the gateway decision. This is the default for unlisted hosts.</li>
        <li><strong>ALLOW.</strong> Permit explicitly configured calls with a rate limit.</li>
        <li><strong>SANDBOX.</strong> Use test credentials and reject a live key detected on the way out.</li>
        <li><strong>CAPTURE.</strong> Store email or SMS in a searchable test inbox. An agent can retrieve a magic link and complete a sign-in flow.</li>
        <li><strong>MOCK.</strong> Return configured responses. The built-in Stripe pack maintains state across supported payment and subscription flows.</li>
        <li><strong>EMULATE.</strong> Route calls to a service such as LocalStack or Azurite running inside the environment, without changing the application's endpoint.</li>
        <li><strong>SYNTH.</strong> Generate a response with a model. Checks that depend on a synthesized response remain unverified.</li>
      </ul>
      <h2>Use stateful mocks for a complete flow</h2>
      <p>A single response fixture is useful for testing one API call. A subscription lifecycle needs more: creating, changing, renewing, and cancelling must affect what later requests return.</p>
      <p>The Stripe pack supports checkout, subscriptions, renewals, and cancellations with signed webhooks. This lets a test exercise the application code that handles those events without reaching a live payment processor.</p>
      <h2>Keep the policy at the network boundary</h2>
      <p>The running application shares a network namespace with a gateway sidecar. Outbound traffic must pass through the gateway, including calls from libraries that ignore proxy environment variables.</p>
      <p>When a new integration introduces an unlisted host, the gateway blocks the request. Configure the appropriate mode before testing that flow again. A denied request is recorded; it does not by itself mean every check in the run failed.</p>
      <h2>Inspect what happened</h2>
      <p>Use the gateway log to see which requests were allowed, captured, mocked, or refused. Direct connections stopped by network isolation never reach the gateway and do not appear as gateway log entries.</p>
      <p><a href="/docs/concepts/egress">Read the egress policy guide</a> for configuration and examples.</p>
    </>
  ),
};
