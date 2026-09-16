# added

`examples/github-reaper-workflow.yml` runs `af env reap --yes` on a schedule, so
cost never depends on a human remembering to sweep. It belongs beside the
workflow that creates environments, on the runner or cluster where they live and
with the same credentials, and it matters most on the Kubernetes runtime, where a
namespace a killed run left up keeps costing money until something removes it.
