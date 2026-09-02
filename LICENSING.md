# Licensing

Two licenses, split by directory.

| Where | License | What it means |
| --- | --- | --- |
| Everything except `ee/` | MIT, in [LICENSE](./LICENSE) | Use it, change it, sell it, run it in production, forever, with no key and no phone home. |
| [`ee/`](./ee) | [Antifailure Enterprise License](./ee/LICENSE.md) | Source visible and modifiable. Running it in production requires a valid license key or subscription. No reselling, and no offering it to third parties as a hosted service. |

The community edition is complete rather than a demo. Masking, verification,
every database provider, the egress and mocking layer, the agent runner,
insights, load, and the control plane are MIT and stay MIT. What is in `ee/` is
the set of things a large company requires before a rollout and an individual
developer never uses: single sign on, SCIM, custom roles, SIEM streaming,
policy enforcement, customer owned runtimes, and billing.

The reasoning behind the split, including the alternatives that were rejected,
is in [ADR 0002](./docs/adr/0002-license-model.md).

## Why this file exists, and why `LICENSE` no longer says it

`LICENSE` used to carry the MIT text followed by a paragraph naming the `ee`
carve out. That paragraph was correct and it made the license undetectable.

GitHub identifies a license with [Licensee][], which normalizes the file and
compares it against known license texts. Appended prose is folded into the
comparison, so the extra paragraph dropped the match below the threshold and
the repository reported `NOASSERTION`: no license in the sidebar, absent from
the license filter, and to anyone scanning it, all rights reserved. For an MIT
licensed tool that is a real loss, and it is the opposite of what the paragraph
was trying to achieve.

The fix is that `LICENSE` is now the unmodified MIT text and the carve out
lives here. Nothing about the boundary changed. It is stated in four other
places, each of which a reader or a scanner reaches independently:

- [`ee/LICENSE.md`](./ee/LICENSE.md), the terms themselves, which also say that
  everything outside `ee/` is MIT and carries none of the restrictions.
- [`ee/README.md`](./ee/README.md), which opens by saying the directory is not
  MIT licensed.
- A header on every source file in `ee/`, naming the license and pointing at
  `ee/LICENSE.md`.
- [ADR 0002](./docs/adr/0002-license-model.md).

A directory scoped license file is the ordinary convention for this and is what
license scanners walk the tree to find.

One thing was deliberately not done. Licensee ignores HTML comments, so
wrapping the old paragraph in one would have restored detection while leaving
the text in `LICENSE`. That was rejected: the tools it would hide the carve out
from are the same ones companies run before adopting something, so a scan would
have reported the whole repository as MIT and missed the `ee` restriction
entirely. A green badge is not worth a boundary that automated review cannot
see.

[Licensee]: https://github.com/licensee/licensee
