// The Developer Platform lane's pure helpers.
//
// No imports, on purpose. Console unit tests are `node --test lib/*.test.ts`,
// so anything that has to be tested has to load outside a browser, and every
// decision worth arguing about on this lane is in here rather than inline in a
// cell where nothing could reach it. Each of the four functions below answers a
// question the obvious version gets wrong, and platform-format.test.ts is where
// the wrong answers are written down.

/** live, expired or revoked. Declared here rather than in admin-platform.ts so
 *  that the module a test can load carries its own vocabulary, and re-exported
 *  from there so a page still imports one thing. */
export type CredentialStanding = "live" | "expired" | "revoked";

/**
 * The tone a credential's standing should wear.
 *
 * Its own function rather than `toneFor`, because `toneFor` maps engine states
 * and would read "revoked" as an ordinary word. A revoked credential is not a
 * failure and must not be red: somebody revoked it on purpose, usually the
 * person now reading the list to check that it worked. Expired is the one that
 * deserves attention, because nothing decided it.
 */
export function standingTone(standing: CredentialStanding): "pass" | "warn" | "neutral" {
  if (standing === "live") return "pass";
  if (standing === "expired") return "warn";
  return "neutral";
}

/**
 * What to say about an approval, given a pull request.
 *
 * Three answers rather than two. "Approved" and "not approved" hides the state
 * that matters: an approval recorded against a commit that is no longer at the
 * head. 0021 stores approved_sha rather than a boolean for exactly that reason,
 * and collapsing it back to a boolean here would undo it.
 */
export function approvalWording(pr: {
  fromFork: boolean;
  approvedSha: string | null;
  approvalCoversHead: boolean;
}): { label: string; tone: "pass" | "warn" | "neutral"; hint: string | null } {
  if (!pr.fromFork) {
    return {
      label: "not required",
      tone: "neutral",
      hint: "The head branch is in this repository, so no approval gate applies.",
    };
  }
  if (pr.approvedSha === null) {
    return {
      label: "not approved",
      tone: "warn",
      hint: "From a fork and never approved, so nothing has run against it.",
    };
  }
  if (!pr.approvalCoversHead) {
    return {
      label: "stale approval",
      tone: "warn",
      hint: "The approved commit is not the one at the head now. A push landed after the approval.",
    };
  }
  return { label: "approved", tone: "pass", hint: null };
}

/** A commit, shortened the way git shortens one. Kept here rather than inline
 *  so the two screens that show a head cannot pick different lengths. */
export function shortSha(sha: string | null): string | null {
  return sha === null ? null : sha.slice(0, 7);
}

/**
 * Whether a delivery was handled, in one word.
 *
 * The two ledgers disagree about how they say it, and this is the one place
 * that reconciles them. A GitHub delivery is handled when handled_at is set; a
 * Stripe event carries an outcome, and 'unresolved' means the webhook could not
 * decide what it meant, which is not the same as not having been looked at.
 */
export function deliveryStanding(d: {
  handledAt: string | null;
  outcome: string | null;
}): { label: string; tone: "pass" | "warn" | "fail" } {
  if (d.outcome === "unresolved") return { label: "unresolved", tone: "warn" };
  if (d.handledAt === null) return { label: "not handled", tone: "warn" };
  if (d.outcome === "stale") return { label: "stale", tone: "warn" };
  return { label: d.outcome ?? "handled", tone: "pass" };
}
