# added

The four legal documents a security review asks for by name are now published
on antifailure.dev: a Data Processing Agreement at `/dpa`, the subprocessor list
at `/subprocessors`, a statement that there is no service level agreement at
`/sla`, and retention and deletion commitments at `/data-retention`. All four
are linked from the footer.

Every subprocessor was established by reading the code that talks to the vendor:
Microsoft and GitHub for every organization, Anthropic and OpenAI only when an
organization stores a model provider key, and no payment, email, or analytics
vendor at all. Every retention period is one the software already enforces,
including the two that are not exact.

The privacy notice claimed the control plane holds billing data, which no
payment processor exists to produce, and did not mention the waitlist address it
stores or the address and user agent a session row carries. All three are fixed.

No lawyer has read any of it, and every page says so on its face.
