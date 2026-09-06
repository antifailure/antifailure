# fixed

`af start` reported a machine that could run `af up` as blocked, named a
secret as the next step, and refused to look at the goldens that made the
secret unnecessary.

The golden step was declined for every provider with one reason, that listing
goldens takes the branch's lock. That is true of the orchestrator's listing and
was never true of the Docker provider's, which is an image list on the daemon
and needs no lock, no `.antifailure` directory and no state database. So a
machine holding five verified goldens for the project was told "whether a
golden exists was not checked". The step now lists the daemon directly and
selects with the same rule `af up` uses, verified and made for this project,
so it never names another project's golden or an unverified one. It says the
version, when it was made and that it is this project's. With none usable it
is pending with `af golden refresh`, or with `af up` when no source is named,
since the first `af up` builds that golden itself. A hosted provider's step is
still declined, with its reason, because that listing needs credentials.

The source step was blocked whenever the variable naming production was unset.
When a usable golden exists, `af up` branches it and the scheduled refresh is
skipped until the variable holds something, so the variable is needed by the
next refresh rather than by the next command. That step is now a warning that
names the golden and the schedule, the Next section says `af up`, and the exit
code is zero. It is blocked only when there is no golden and no source.
The masking rules step is demoted the same way: an absent `masking.yaml`
beside a usable golden is a warning about the next refresh, since `af up`
branches the golden as it is, and it stays pending with `af mask init` only
when there is no golden yet.

Every place that states the first run now states one sequence: install,
`af init`, then `af golden refresh` only when the manifest names a production
database, then `af up` and `af test`. The README, the documentation index and
quickstart, the site, the console's CLI page and the hint `af init` prints had
between them four different sequences, and none of them said which step was
conditional or when.
