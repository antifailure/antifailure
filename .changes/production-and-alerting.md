# added

The hosted control plane can now be alerted on and stood up in production.
Eleven Azure Monitor rules and one action group where there were none: an
availability test against `/readyz` from three locations outside the stack,
server errors, restart loops, replicas below minimum, database storage,
connections, CPU and reachability, one rule per container app job, and
certificate expiry. Each rule names its runbook in the notification it sends,
and every runbook is a page under Self-hosting.

`production.tfvars` sits beside `staging.tfvars` and explains every value that
differs, including the recovery point objective next to the retention that is
constantly mistaken for it. `app.antifailure.dev` is Terraform's now: the DNS
records, the managed certificate and the custom domain binding. Standing up
production has a page with the eight things Terraform cannot do, in the order
that works.
