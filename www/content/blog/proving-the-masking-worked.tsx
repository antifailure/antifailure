import type { Post } from "@/lib/blog";

export const MASKING_ATTESTATION: Post = {
  slug: "proving-the-masking-worked",
  title: "Check the data after masking it",
  dek: "Masking rules can miss new columns and copied personal details. A read-back scan checks the result before a test environment uses it.",
  summary: "How deterministic masking, read-back scanning, and signed attestations prepare data for testing.",
  published: "2026-08-28",
  updated: "2026-09-26",
  tags: ["Postgres", "Data masking", "Privacy"],
  body: (
    <>
      <p>Replacing an email address in one column does not remove every copy of it. The same detail may appear on an invoice, inside a webhook payload, or in an old support note.</p>
      <p>Masking needs two stages: transform the data, then inspect the result. Antifailure keeps both stages inside your infrastructure.</p>
      <h2>Where personal details can remain</h2>
      <ul>
        <li><strong>Copied fields.</strong> An invoice may preserve the billing address from the time of purchase.</li>
        <li><strong>JSON payloads.</strong> Stored API responses can contain details several levels deep.</li>
        <li><strong>Free text.</strong> Notes and messages may contain phone numbers, addresses, or keys.</li>
        <li><strong>Audit history.</strong> Event records can retain values that have since changed elsewhere.</li>
        <li><strong>New columns.</strong> Schema changes can add fields that existing masking rules do not cover.</li>
      </ul>
      <h2>Preserve relationships while replacing identities</h2>
      <p>If two related values are replaced independently, a join or application workflow can break before the test reaches the code you changed. Deterministic masking maps the same input to the same replacement across tables and refreshes.</p>
      <p>Antifailure compiles masking rules to SQL and runs them in resumable chunks. Related data stays consistent, and an interrupted masking operation can resume.</p>
      <h2>Read back the result</h2>
      <p>After masking, a scanner checks every table and column for values that resemble emails, card numbers, phone numbers, or keys. It samples up to 2,000 rows per column by default.</p>
      <p>The sample size matters. This check can identify an unmasked column, but it does not prove that every row is free of sensitive data. The signed attestation records the sample size and findings so the scope is visible.</p>
      <h2>Verify before branching</h2>
      <p>A reusable snapshot, called a golden, must pass verification before Antifailure can branch it. A failed scan stops environment creation so your team can correct the masking rules first.</p>
      <p>Keep the attestation with the run evidence. It records what was checked and gives reviewers a concrete basis for deciding whether the data is ready for testing.</p>
      <p><a href="/product/safe-state">See the data preparation flow</a> or read the <a href="/docs/security/data-boundary">data boundary guide</a>.</p>
    </>
  ),
};
