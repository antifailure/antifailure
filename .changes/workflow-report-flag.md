# fixed

`examples/github-workflow.yml`, the file the documentation tells you to copy to
`.github/workflows/antifailure.yml`, ran `af ci --output report.md`. That flag
was renamed to `--report` when `--output` became the persistent format flag, and
the example was not updated with it, so the copied workflow stopped at
`the output format "report.md" is not recognised` before `af ci` did any work.
Nothing brought an environment up and no comment was ever left on the pull
request.

The gate that checks documented commands against the real command tree could
not have caught it twice over: it reads only `docs/src/content/docs`, and it
asked whether a flag exists rather than whether the command accepts the value.
`--output` does exist. It now also reads the workflow files under `examples/`
and `.github/workflows/`, and it parses each invocation and runs the same
validation the binary runs, so a flag that exists and refuses its value is a
finding rather than a pass.
