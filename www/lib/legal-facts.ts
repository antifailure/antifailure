/**
 * The numbers the legal pages publish, in one place, checked against the
 * infrastructure that decides them.
 *
 * WHY THIS FILE EXISTS. Seven published claims were found false in one night,
 * across privacy, retention, subprocessors and deletion. Backup retention said
 * fourteen days while production runs thirty-five. Log retention was documented
 * nowhere. The subprocessor page said there was no billing and that nothing
 * could send mail, while the repository held a real Stripe client and a real
 * mailer. Provider-key removal was called deletion when it is revocation.
 *
 * Every one of them was TRUE WHEN IT WAS WRITTEN. Not one was a mistake at the
 * time. They drifted because the code moved and prose does not have a compiler,
 * and a legal page that has drifted is worse than a documentation page that has
 * drifted, because somebody relies on it in a way they cannot check.
 *
 * So the numbers live here and a test compares them to the Terraform that sets
 * them. Changing production's retention without changing this file fails that
 * test, which is the same shape as config-docs.test.ts holding the control
 * plane's environment variables to the source that reads them.
 *
 * WHAT THIS CANNOT DO, said here rather than discovered later. It holds NUMBERS
 * and the presence of NAMED CAPABILITIES. It cannot hold a sentence: "we do not
 * use Stripe" and "Stripe cannot be used" differ by a promise, and no gate is
 * going to tell those apart. Those stay a judgement, and the rule the
 * subprocessor page now follows is written at the top of subprocessors.ts.
 */

/** Words, because the legal pages are written in prose and a digit in the
 *  middle of a sentence reads like a form. The test holds the pair together so
 *  the words cannot drift from the number they spell. */
export interface RetentionFact {
  days: number;
  words: string;
}

/**
 * Point-in-time recovery on the hosted database.
 *
 * Production and staging genuinely differ, and the pages that said a flat
 * fourteen were describing staging while a reader took them for production.
 * Both are published, because "thirty-five days" alone is the claim somebody
 * would rely on for a staging incident.
 */
export const BACKUP_RECOVERY = {
  production: { days: 35, words: "thirty-five" } satisfies RetentionFact,
  staging: { days: 14, words: "fourteen" } satisfies RetentionFact,
};

/** Operational logs in Azure Monitor. Documented nowhere at all until now,
 *  which is its own kind of drift: a retention period nobody published is one
 *  nobody can hold anybody to. */
export const LOG_RETENTION = {
  production: { days: 90, words: "ninety" } satisfies RetentionFact,
  staging: { days: 30, words: "thirty" } satisfies RetentionFact,
};

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
    // main.ts rather than mail.ts: the mailer takes its key as a constructor
    // argument and main.ts is what reads the environment, refusing to start on
    // a half-configured set rather than sending nowhere.
    module: "web/apps/api/src/main.ts",
    variables: ["AF_RESEND_API_KEY", "AF_MAIL_FROM"],
  },
];
