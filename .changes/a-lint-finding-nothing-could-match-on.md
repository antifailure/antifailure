# added

A lint finding now carries an identifier that does not move between releases.

The release notes said the stable identifier for a finding was its rule name
within a release, which is another way of saying there was no stable identifier
at all. Anything filtering, suppressing or counting a finding had to match on a
name that the next release was free to rewrite, and rewriting names is what
improving a rule looks like: the day `index_not_concurrent` grew a sibling for
the drop and another for the rebuild, a filter written against it was matching
a different set of statements than its author meant.

The rule names were also not published anywhere. The insights page describes
each rule in a sentence of prose, so somebody reading `"rule":
"unique_constraint_builds_index"` out of `--output json` had no page to look it
up on.

Every finding now carries a `LINT-NNN` identifier beside the rule name:

    {"id": "LINT-010", "rule": "unique_constraint_builds_index", ...}

The report a person reads leads with it too, so the thing to write down is the
thing on the page:

    What these migrations do to a table this size:
      LINT-016  table dropped
        on orders, about 40000000 rows

The identifier is assigned once and keeps its meaning. It is never reused, not
even after the rule that earned it is deleted: a retired rule keeps its entry
and its number, so a filter written against one never quietly starts matching
something else. The rule name, the title, the explanation and the fix all stay
free to change, because that is what makes a rule better.

`engine/internal/insights/lintcatalog.yaml` is the source of truth. The Go
code, the new lint findings reference page and the published catalogue at
antifailure.dev/lint-findings.v1.json are generated from it, and
`findings.register.json` beside it records every identifier ever handed out.
`GET antifailure.dev/api` lists the catalogue beside the error one, so an agent
asking what this host offers a machine is told about it.
`just lintcheck` runs in CI and refuses a rule with no identifier, an entry for
a rule that no longer exists, a duplicate, and an identifier that has left the
catalogue since it was registered.

Nothing about which findings a release reports has changed.
