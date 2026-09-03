# added

The terms of use now carry the three sections they were missing: what the
software is allowed to touch, a warranty disclaimer, and the shape of a
limitation of liability. Two new pages join them, an acceptable use policy at
`/acceptable-use` and a developer policy at `/developer-policy` covering the
control plane API and the engine's Model Context Protocol surface.

The liability section publishes no cap. The contracting entity, the registered
address, the governing law and the figure itself are rendered as visible blanks,
because a cap written before a lawyer has chosen the jurisdiction it will be
read in is a number rather than a protection. The page says which exclusions no
contract can make, and says plainly that whether any of it is enforceable
depends on the jurisdiction, on whether the customer is a business or a
consumer, and on whether the harm was caused by our own negligence.

What the terms claim about the engine is now held to the engine. `legal-facts`
gained seven assertions: that the customer's source database is opened in a
transaction Postgres has marked read only, that teardown refuses a container
Antifailure did not label, that `NewRuleSet` still appends the default masking
rules so an unconfigured project is not an unmasked one, and that a golden
failing verification is still never published. Each one was checked by breaking
the guard on purpose and watching the assertion go red.

The eighth pins the list of column types the verification scan reads. The terms
describe that scan as a check that a masking rule missed a column rather than a
proof that no personal data survives, and that wording is exact on purpose:
the scan's type list and the masking default's type list are the same six
entries, so a column outside them is read by neither. Changing either list now
sends whoever changed it to the sentence describing it.

No lawyer has read any of it, and every page says so on its face.
