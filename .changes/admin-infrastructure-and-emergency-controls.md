# added

Operators can now see what the installation is doing across every organization,
and stop it. The portal gains system health, the fleet of running environments,
the teardown ledger, the egress firewall, and three emergency switches.

Health is a list of checks rather than a colour, and each one says what was
counted, which number would be wrong, and what to do when it is. It reports
environments that outlived their expiry and are still holding a database branch
and containers, teardowns waiting on a runtime, teardowns abandoned after every
attempt failed, the delay between an event happening and arriving, GitHub
deliveries accepted and never handled, and pull request checks past their own
deadline.

The teardown ledger keeps apart three things a single word hides. A request
that was RECORDED with no workflow run and no environment id had nothing to
send and will sit until it is abandoned. A request that was DISPATCHED has been
asked for and not confirmed, because a cancel GitHub accepts is not a runtime
saying the environment is gone. A CONFIRMED request is one the runtime
acknowledged. An operator asking for a whole fleet to be torn down is told, per
environment, which of those it is.

The firewall view reports one condition as always failing rather than scoring
it: an egress rule in sandbox mode with no sandbox credential configured
forwards whatever credential the application set, to the real provider. Such a
request is identical to a working sandbox call in every column of the request
log except that nothing was substituted.

The three switches are maintenance mode, new sign-ups, and new runs. Each is
enforced by a named function with a real call site, refuses at a path a test
drives, and is released from the same screen that engaged it. Maintenance keeps
reads, sign-in and event ingestion working, so the operator who paused the
installation can still authenticate to unpause it and no engine loses the
record of work that ran anyway. Pausing sign-ups refuses only accounts the
installation has never seen, so nobody mid-task is locked out. Freezing runs
leaves teardown working, because an operator freezing during an incident still
has to be able to stop what is running.

Engaging or releasing a switch writes a high severity audit entry in the same
transaction as the change, with the entry first, so a switch cannot take effect
without its record and a rollback takes both.
