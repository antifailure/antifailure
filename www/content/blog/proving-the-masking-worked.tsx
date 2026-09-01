import type { Post } from "@/lib/blog";

/**
 * Grounded in the README's "Masked data, verified" section and the privacy
 * page: masking compiled to SQL, executed in resumable chunks, deterministic
 * across tables and refreshes, a scanner that reads back every column of every
 * table, a signed attestation, and the rule that an unverified golden cannot
 * be branched. Nothing here is invented for the post.
 */
export const MASKING_ATTESTATION: Post = {
  slug: "proving-the-masking-worked",
  title: "Masking data is easy. Proving it worked is the product.",
  dek: "Any UPDATE statement can overwrite an email column. The hard part is showing that nothing identifying survived anywhere, and that nobody can skip the check.",
  summary:
    "Why anonymisation needs verification and enforcement rather than a script: determinism, read-back scanning, and a gate that cannot be waived.",
  published: "2026-08-28",
  tags: ["Postgres", "Data masking", "Privacy"],
  body: (
    <>
      <p>
        Writing the masking is the part people budget for. It is also the part
        that takes an afternoon. An <code>UPDATE</code> that overwrites{" "}
        <code>users.email</code> with a generated address is not difficult, and
        most teams already have one.
      </p>
      <p>
        The failure is never in that statement. It is in the eleven other places
        the same address also lives, and in the fact that nothing checked.
      </p>

      <h2>Where identifying data actually survives</h2>
      <p>
        A masking script written against the schema masks the columns somebody
        thought of. In practice the ones that get missed are the ones that are
        not shaped like a person:
      </p>
      <ul>
        <li>
          <strong>Denormalised copies.</strong> A <code>billing_email</code> on
          the invoice, captured at the time of purchase so it would not change.
        </li>
        <li>
          <strong>JSON columns.</strong> A webhook payload stored whole in a{" "}
          <code>jsonb</code> column, with the customer&apos;s address nested
          three levels down where no column-level rule reaches.
        </li>
        <li>
          <strong>Free text.</strong> A support note reading &ldquo;called them
          on 555-0142, will retry Tuesday.&rdquo;
        </li>
        <li>
          <strong>Audit and event tables.</strong> Append-only by design,
          frequently the largest tables in the database, and routinely excluded
          from a masking pass because they are awkward.
        </li>
        <li>
          <strong>Anything added since.</strong> The column somebody shipped
          last month, which the masking config predates.
        </li>
      </ul>
      <p>
        That last one is the structural problem. A masking configuration is a
        list, the schema is a moving target, and a list maintained by hand
        against a moving target is wrong within a quarter. It fails silently,
        because a missed column produces no error. It produces a database that
        looks anonymised.
      </p>

      <h2>Determinism, and why it is not optional</h2>
      <p>
        Random replacement destroys the data. If <code>orders.customer_email</code>{" "}
        and <code>users.email</code> are masked independently, the join breaks
        and a checkout flow that depends on it no longer exercises anything. The
        environment stops reproducing the behaviour you built it to reproduce.
      </p>
      <p>
        So the mapping has to be deterministic: the same input produces the same
        fake output, in every table, on every refresh. That preserves
        referential integrity across the whole database and means a bug found
        against one branch is reproducible against the next one.
      </p>
      <p>
        Antifailure compiles masking to SQL and executes it in resumable chunks,
        deterministically, so the same customer maps to the same fake customer
        across every table and every refresh. Resumable matters more than it
        sounds: at production row counts these operations run long enough that
        something will interrupt one, and a masking pass that cannot resume is a
        masking pass that gets skipped under time pressure.
      </p>

      <h2>The read-back</h2>
      <p>
        Everything above is still just a better script, and a better script is
        still unverified. The check that matters runs afterwards and asks a
        different question. Not &ldquo;did the rules execute&rdquo; but
        &ldquo;is there anything identifying left.&rdquo;
      </p>
      <p>
        So a scanner reads back every column of every table looking for anything
        that still parses as an email, a card number, a phone number or a key.
        Every column, including the ones nobody configured, which is the entire
        point: the column added last month is exactly the one the configuration
        does not know about, and a read-back does not need to know about it in
        advance.
      </p>
      <p>
        Every column, and a sample of the rows. The scan takes up to two
        thousand rows per column by default and the attestation records the
        number it used, so what it proves is bounded and the bound is written
        down. That is a deliberate trade and it is worth saying out loud rather
        than leaving in the code: the failure this is built to catch is a rule
        that missed a column entirely, which shows up in the first hundred rows,
        and a full read of a wide schema turns seconds into minutes. A single
        real value hiding in one row out of a million is not what this finds.
      </p>
      <p>
        It then signs an attestation, which turns the result into something that
        can be checked later rather than a line in a log that scrolled past.
      </p>

      <h2>A check nobody can skip</h2>
      <p>
        The last part is the one that decides whether any of this holds up. A
        verification step that can be waived will be waived, at the worst
        possible moment, by somebody reasonable who needs an environment before
        a demo.
      </p>
      <p>
        So the rule is that an unverified golden cannot be branched, and it is
        enforced in code rather than in a checklist. There is no flag. A dataset
        that has not passed the scan is not available to branch from, which
        means the failure mode is an environment you cannot create rather than
        an environment full of real customer data that looks fine.
      </p>
      <p>
        That inversion is the whole design. Checklists document intent. Gates
        produce outcomes. The difference shows up on the day somebody is in a
        hurry, which is the only day it was ever going to matter.
      </p>
    </>
  ),
};
