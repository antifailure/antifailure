# changed

The self-hosting and enterprise pages a self-hoster reads first are shorter by 685
lines, with no fact removed. What went: sentences restating the sentence before
them, rationale for decisions a reader operating the system does not act on, the
history of how a page came to say what it says, and reassurance.

Every command, flag, `AF_` variable, path, port, code block, Terraform variable,
Helm value, error code and number is still there. Where a proposed cut would have
taken one of those, the cut was dropped instead: 95 of 358 candidates were refused,
56 of them because the line range would have broken a sentence in half.

Two miscounts went with the padding. `production.md` said the checklist is nine
things where it is fifteen steps. The `licensegen` receipt was described as warning
that `rbac` is gated nowhere, which two other pages record as deleted once custom
roles became real.
