# fixed

The self hosting pages pinned a control plane image that could not create the
first organization.

`main-b53906a` predated `create-org`, `break-glass` and the `tokens.manage`
scope, so a self-hoster who followed the four step bring-up got a running server
with no organization and no way to make one, which is the exact broken state the
page warns about. The pages now pin `main-fa6c8aa`, whose image carries those
commands. The stale "do not run `:latest` or `:v0.1.1`, they are the same image"
note is gone, because `:latest` is now a newer image than that note described.
The Kubernetes section gains the `create-org` step the bootstrap Job does not
run.
