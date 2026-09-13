# fixed

The GitHub Action failed the pull request check of every repository that used
the documented workflow, in about fourteen seconds and before `af` ran, with
`jq: parse error: Unmatched '}' at line 1, column 3`. That has been true of every
release since v1.3.0, so it is on `antifailure/antifailure@v1`.

The action's first step read its dispatch input with `${AF_DISPATCH:-{}}`, and a
shell reads that as the default `{` followed by a literal `}`. Any dispatch that
was not empty gained a closing brace. The documented workflow passes
`toJSON(inputs)`, which is `{}` on a pull request, and a workflow that leaves the
input out gets the input's declared default, which is also `{}`. Both reached
`jq` as `{}}`. So did any other JSON a caller passed. Only a workflow passing the
input explicitly as an empty string got past that line. The copies of the
example that `af init` and the GitHub App write pass `toJSON(inputs)`, so they
failed too.

The step now reads the input unchanged and treats an empty or `null` dispatch as
none. `tools/actioncheck` runs that step against every dispatch a caller can
send, including an explicit empty string and the value GitHub itself renders for
`toJSON(inputs)`.

A workflow pinned to `antifailure/antifailure@v1` receives the fix when the `v1`
tag moves, which happens on the next final release.
