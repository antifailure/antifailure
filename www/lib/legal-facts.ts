/**
 * The capabilities the legal pages publish, in one place, checked against the
 * code that provides them.
 *
 * WHY THIS FILE EXISTS. Seven published claims were found false in one night.
 * The legal pages said there was no billing and that nothing could send mail,
 * while the repository held a real Stripe client and a real mailer. Provider-key
 * removal was called deletion when it is revocation.
 *
 * Every one of them was TRUE WHEN IT WAS WRITTEN. Not one was a mistake at the
 * time. They drifted because the code moved and prose does not have a compiler,
 * and a legal page that has drifted is worse than a documentation page that has
 * drifted, because somebody relies on it in a way they cannot check.
 *
 * So the capabilities live here and a test compares them to the code that
 * provides them. Removing or renaming an integration without changing this file
 * fails that test, which is the same shape as config-docs.test.ts holding the
 * control plane's environment variables to the source that reads them.
 *
 * WHAT THIS CANNOT DO, said here rather than discovered later. It holds the
 * presence of NAMED CAPABILITIES. It cannot hold a sentence: "we do not use
 * Stripe" and "Stripe cannot be used" differ by a promise, and no gate is going
 * to tell those apart. Those stay a judgement.
 */

/**
 * Capabilities the control plane's code CONTAINS, each one inert until the
 * variables beside it are set.
 *
 * The distinction this encodes is the one the subprocessor page kept getting
 * wrong: whether the software can reach a vendor is a fact about the
 * repository, and whether a given deployment does is a fact about an
 * environment nobody reading the page can inspect. The page may state the
 * first. It may not state the second.
 *
 * The test asserts each `module` exists and mentions each `variables` entry, so
 * an integration that is removed, or renamed, fails rather than leaving the
 * page describing a vendor that is no longer reachable.
 */
export interface ConditionalProcessor {
  vendor: string;
  module: string;
  variables: string[];
}

export const CONDITIONAL_PROCESSORS: ConditionalProcessor[] = [
  {
    vendor: "Stripe",
    module: "web/apps/api/src/billing/plans.ts",
    variables: ["AF_STRIPE_SECRET_KEY", "AF_STRIPE_WEBHOOK_SECRET"],
  },
  {
    vendor: "Resend",
    // boot.ts rather than mail.ts: the mailer takes its key as a constructor
    // argument and boot.ts is what reads the environment, refusing to start on
    // a half-configured set rather than sending nowhere.
    //
    // It was main.ts until the control plane's entry point was split so that a
    // second edition could register its routes without a second copy of the
    // configuration. Every line that reads the environment moved to boot.ts and
    // main.ts is now the community entry point and one call. This is a legal
    // page's claim about where a vendor is engaged, so it names the file that
    // engages it rather than the file that starts the process.
    module: "web/apps/api/src/boot.ts",
    variables: ["AF_RESEND_API_KEY", "AF_MAIL_FROM"],
  },
];
