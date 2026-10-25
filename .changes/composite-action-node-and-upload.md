# changed

The composite action that a workflow runs with `uses: antifailure/antifailure@v1`
now installs Node with `actions/setup-node` v7.1.0, up from v7.0.0, and keeps the
workload result with `actions/upload-artifact` v7.0.2, up from v7.0.1. Both are
patch releases of the actions it already used, pinned to their commit, and the
action's inputs and outputs are unchanged.
