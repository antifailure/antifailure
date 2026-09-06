# fixed

The error reference says scripts can branch on the exit codes, that they are
stable, and that 4 means authentication or authorization failed. `af whoami`
and `af provider list` honoured that when nothing was signed in: AF-CPL-004,
a Next line naming the `af login` command, a docs link, exit 4. `af token
list`, `af token create` and `af token rm` did not. The same absent credential
produced a bare sentence with no code, no Next line and no link, and exit 1,
so a script that branched on 4 to decide whether to sign in treated every
token subcommand as a generic failure. The three other reasons a stored sign
in is unusable, expired, no longer accepted by the control plane, and short a
scope, were bare sentences and exit 1 on every command that reads one.

Every command that reads the stored sign in now refuses with one of four
codes, all exit 4: AF-CPL-004 for none, AF-CPL-005 for expired, AF-CPL-006
when the control plane no longer accepts it, AF-CPL-007 when it does not
carry the scope the command needs. Each names the `af login` invocation that
fixes it, with the scope the command needs where there is one.

`af init` on a repository that already had a manifest reused AF-MAN-002,
whose next step is to fix the reported line and run `af doctor`. There was no
reported line, the file was usually valid, and `af doctor` cannot change the
one fact that stopped the command. The flag that does was never named. It is
now AF-MAN-007, which names the file and `af init --force`, and says that
`--force` discards every edit rather than merging. The flag's own help text
said "instead of merging into it", and nothing merges; it now says what
happens.

`af env list --output json` called the services column `name`. The key held
the comma joined service list, and the identifier was already in `env_id`.
It is now `services`. The field was never among the documented fields of the
command's JSON output, which is what the stability page promises to keep, and
nothing in this repository read it, so it is renamed rather than carried as
an alias.
