# fixed

The dogfood check posted its whole report to the control plane's `/v1/pr/report`
callback, and the report had outgrown the endpoint's body limit, so the post was
refused with a 413 and the check went red having verified the change perfectly
well. The report was about three and a half megabytes against a one megabyte
limit, and effectively all of it was one field: the exploration evidence, the
captured response bodies and DOM snapshots of every browser goal, which the
control plane never reads. It reads counts and a little metadata off the report,
and from an exploration result only its name, verdict, visited pages and whether
a trace exists.

So the workflow now drops each exploration result's evidence down to its trace
before it posts, which takes the body from three and a half megabytes to about
ninety seven kilobytes and leaves every field the control plane reads untouched.
Measured on a real report, the decoded counts are identical before and after the
trim. The full report, evidence and all, is still uploaded as the run's
artifact. `exploration-report.test.ts` guards the server half of the contract:
if the decoder ever starts reading another evidence field, the trim would drop a
field the verdict depends on, and that test goes red.

This is internal: `/v1/pr/report` is the dogfood self-check's own callback and
has no other caller, and a customer's engine reports through `/v1/events`.
