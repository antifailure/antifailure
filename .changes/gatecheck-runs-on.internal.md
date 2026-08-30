# fixed

`tools/gatecheck` now refuses a workflow whose job declares neither `runs-on`
nor `uses`. GitHub rejects such a file before it creates a single job, so the
run carries no jobs and no check appears on the pull request at all. The gate
that goes missing is quieter than the gate that goes red, and the existing
check that every workflow is valid YAML cannot see it, because the YAML is
valid and it is the Actions schema that is not.
