# fixed

A workflow's `budget` bounded nothing. `budget.steps` and `budget.duration`
were normalised to 60 steps and ten minutes and refused when malformed, and the
document sent to the runner carried neither. Every workflow ran to the runner's
own forty steps and for as long as that took. A page that never answered held a
workflow for the browser's thirty second timeout on every attempt, and the
workflow was then judged on whatever was on the screen. The documentation
quoted `AF-AGT-002 Workflow sign-up exhausted its budget of 40 steps`, which
nothing ever produced, and AF-AGT-002 itself told every failed workflow it had
"exhausted its budget of its attempts", so a plain failure read as a budget
problem.

Both halves now reach the runner from every command that runs workflows: `af
test`, `af ci`, the `run_browser_workflows` tool and the hosted runner. The time
budget covers the whole workflow, retries included, and it is a hard cap. A
workflow that reaches it is stopped where it is and ends as blocked with the
budget named, and no further attempt starts. A workflow that uses every step
passes if everything it expected is visible on the page it reached, fails if
that page answered with an HTTP error, and otherwise ends as blocked with the
step budget named. A blocked workflow is never a partial pass.

A workflow stopped by its budget has its own code, AF-AGT-024, which names the
workflow and the budget. `af test` and `af ci` raise it when that is why nothing
was verified, and `af ci` shows the workflow as blocked, never as passed or
failed. AF-AGT-002 now says a failed workflow failed.
