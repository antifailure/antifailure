// What the App did about each repository's workflow file, in words.
//
// The control plane records a state word per repository and the page needs a
// sentence, a tone and sometimes a link. Kept out of the component so the
// mapping is testable without a renderer, and so a new state on the server
// side lands as a neutral line with the state word rather than a blank.

import type { Tone } from "@/components/ui";

export interface RepositorySetup {
  id: string;
  repository: string;
  state: string;
  attempts: number;
  branch: string | null;
  pull_request_number: number | null;
  pull_request_url: string | null;
  last_error: string | null;
  requested_at: string;
  finished_at: string | null;
}

export interface SetupLine {
  /** The status, as a short phrase a person reads beside the repository. */
  label: string;
  tone: Tone;
  /** Where the label goes, when it goes anywhere: the pull request. */
  href: string | null;
  /** The sentence under the label, when there is one worth reading: the
   *  remedy for a missing permission, or the last error. */
  detail: string | null;
}

export function describeSetup(row: RepositorySetup): SetupLine {
  switch (row.state) {
    case "queued":
    case "leased":
      return { label: "Opening a pull request", tone: "warn", href: null, detail: null };
    case "opened":
      return {
        label:
          row.pull_request_number === null
            ? "Pull request opened"
            : `Pull request #${row.pull_request_number} opened`,
        tone: "pass",
        href: row.pull_request_url,
        detail: "Merge it and every pull request gets a check.",
      };
    case "present":
      return { label: "Workflow present", tone: "pass", href: null, detail: null };
    case "needs_permission":
      return {
        label: "Needs Contents: write on the App installation",
        tone: "warn",
        href: null,
        detail: row.last_error,
      };
    case "failed":
      return {
        label: "Could not open a pull request",
        tone: "fail",
        href: null,
        detail: row.last_error,
      };
    case "skipped":
      return { label: "Skipped", tone: "neutral", href: null, detail: row.last_error };
    default:
      // A state this console does not know. Shown as it arrived rather than
      // hidden: a newer control plane's word is more useful than nothing.
      return { label: row.state.replace(/_/g, " "), tone: "neutral", href: null, detail: null };
  }
}

/**
 * Whether the list is worth a card at all.
 *
 * A repository whose workflow is present has nothing to get connected about.
 * Everything else does, including an opened pull request: until it is merged,
 * the repository is connected and unchecked, which is the gap the card exists
 * to make visible.
 */
export function stillConnecting(rows: readonly RepositorySetup[]): boolean {
  return rows.some((row) => row.state !== "present");
}
