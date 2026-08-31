import type { Post } from "@/lib/blog";

/**
 * Grounded in the README's "A network you control" section: a sidecar owning
 * the network namespace, the per-host modes with exactly these behaviours, the
 * tripwire on a live key under SANDBOX, the searchable inbox under CAPTURE,
 * and the Stripe pack being complete enough to run checkout, subscribe, renew
 * and cancel with signed webhooks and no network.
 *
 * The manifest accepts six modes and the title says five, which is an argument
 * the post now makes rather than a number that is wrong. synth is held out on
 * the merits: runner/src/verdict.ts:60 maps a synthesized response to
 * unverified, and :129 keeps it unverified even when every assertion passed,
 * so it is the one mode whose output cannot be evidence. af-proxy/synth.go
 * says the same from the other side, calling it an escape hatch and telling
 * anyone without a model key to write a fixture instead. The body names it and
 * says why, because a title that counts five while the product has six is only
 * honest if the piece supports it.
 *
 * The slug is deliberately stale and must stay that way. www/lib/blog.ts says
 * never change one after publishing, and a slug is an identifier rather than a
 * claim: nobody reads the arithmetic in a URL bar, and rewriting one costs
 * every link already pointing at it.
 */
export const EGRESS_MODES: Post = {
  slug: "five-answers-to-an-outbound-call",
  title: "There are five useful answers to an outbound HTTP call in a test environment",
  dek: "Most environments have two: let it through, or break. Neither is right for a payment processor, and the gap is where test runs charge real cards.",
  summary:
    "Five of the six per-host egress modes for a pre-production environment, what each is for, and why the default must be to refuse.",
  published: "2026-08-27",
  tags: ["Testing", "Networking", "Third-party APIs"],
  body: (
    <>
      <p>
        A pre-production environment running your real application will try to
        do the things your real application does. It will charge a card, send a
        welcome email, post to a webhook, and call whatever else you have
        integrated. Every one of those is a side effect leaving the boundary,
        and each needs an answer.
      </p>
      <p>
        Most environments offer two. Let it through, which occasionally emails a
        real customer. Or cut the network, which turns every integration into a
        connection error and every test into a test of your retry logic.
      </p>
      <p>Neither is the right answer for a payment processor. There are five.</p>

      <h2>The five</h2>
      <p>
        In Antifailure each host gets one mode, chosen per host rather than
        globally, because a single environment usually needs several of these at
        once.
      </p>
      <ul>
        <li>
          <strong>BLOCK.</strong> Refuse, with a decision you can read. The
          distinction from an unplugged network matters: a refusal that names
          the host and says why is a diagnosis, while a timeout is a mystery
          that costs somebody an afternoon.
        </li>
        <li>
          <strong>ALLOW.</strong> Let it through, with a rate limit. For the
          hosts that genuinely need to be real, and the rate limit is there
          because a load test pointed at somebody else&apos;s API is an
          incident on their side.
        </li>
        <li>
          <strong>SANDBOX.</strong> Swap in test credentials and trip a wire if
          a live key ever appears. The tripwire is the useful half. Sandbox
          credentials are easy; noticing that somebody&apos;s environment is
          holding a production key is the thing that prevents the incident.
        </li>
        <li>
          <strong>CAPTURE.</strong> Record the email or SMS into a searchable
          inbox. This turns a side effect into an assertion: an agent can sign
          in with a magic link because the link is in an inbox it can read,
          rather than the flow ending at &ldquo;check your email.&rdquo;
        </li>
        <li>
          <strong>MOCK.</strong> Answer from a stateful offline pack. Stateful
          is the word carrying the weight, and it is the difference between a
          fixture and a simulator.
        </li>
      </ul>
      <p>
        There is a sixth mode and this post is not counting it. <strong>SYNTH</strong>{" "}
        asks a model to invent the response, which is worth having when a
        provider offers no sandbox and you have no fixture yet and the run
        would otherwise stop dead. It is not an answer in the sense the other
        five are. Anything that touches a synthesized response is reported as
        unverified rather than passed, even when every assertion held, because
        the reply came from a model and not from the thing under test. The
        five above give you a result you can rely on. That one gives you a
        shape to keep moving against, and tells you plainly that nothing you
        just saw is evidence.
      </p>

      <h2>Why stateful mocking is a different thing</h2>
      <p>
        A recorded fixture answers one request with one response. That is enough
        to test a single call and not enough to test a flow, because a real
        integration has a state machine in it. A subscription that has been
        cancelled must answer differently from one that has not. A payment
        intent moves through statuses. A webhook arrives after the call that
        caused it, signed, and your handler verifies that signature.
      </p>
      <p>
        The test for whether a mock is good enough is whether a complete
        lifecycle runs against it. The Stripe pack is complete enough to run
        checkout, subscribe, renew and cancel with signed webhooks and no
        network at all. Signed matters, because a mock that skips signature
        verification is not exercising the code path that runs in production, so
        the one part of the handler most likely to be wrong is the part never
        tested.
      </p>

      <h2>The default is the design</h2>
      <p>
        Choosing a mode per host is a configuration question. What happens to a
        host nobody configured is a design question, and it is the one that determines
        whether the containment holds.
      </p>
      <p>
        An unlisted host fails closed. This is inconvenient in exactly the way
        it should be: adding an integration means the first run stops and tells
        you there is an unnamed host, and you decide what it should be. The
        alternative, defaulting to allow, is a system that is contained only for
        the integrations somebody remembered, and silently uncontained for every
        one added since.
      </p>
      <p>
        The enforcement point matters as much as the default. Every environment
        gets a sidecar that owns its network namespace, and nothing leaves
        except through it. Not a configured proxy the application is asked to
        use, which is a request the application can decline. A namespace it
        cannot route around, so a library making its own connection is subject
        to the same rules as everything else.
      </p>

      <h2>What this buys</h2>
      <p>
        A test suite that can run a real checkout, receive the signed webhook,
        read the confirmation email, and finish, all without a network
        connection or a single real charge. The flows most worth testing are the
        ones with side effects, and they are exactly the ones most environments
        cannot test at all.
      </p>
    </>
  ),
};
