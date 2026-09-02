# fixed

The check on the published OpenAPI document did not run on any branch in a
migration chain.

It is sequenced immediately after `Test` in the control plane job, and `Test`
is red on every branch in such a chain because the numbering gate reads a gap
until the branches ahead of it land. A step after a failed step is skipped, and
GitHub renders a skipped step exactly like one that had nothing to do. So the
control was dark for as long as the queue, and its darkness was invisible,
because the job already said failure for the reason everybody expected.

The cost was real. Mounting the operator router put eighteen admin routes into
the document that antifailure.dev/openapi.json serves, each described as
`security: []` with the sentence "Requires no permission", which is a false
statement about the operator surface aimed at whatever generates a client from
it. This step ran on neither the change that introduced it nor the change that
fixed it. It was found by running the check by hand.

`if: always()` on the step. A gate whose subject is a published document should
not be silenced by an unrelated failure elsewhere in the same job.
