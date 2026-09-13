# fixed

Main went red on the deploy test's retry stance check while every operation it
named was declared. The check piped the deploy script's header into
`grep -q` under `pipefail`. grep leaves at its first match, and once the header
grew past 4096 bytes the process writing it was killed by SIGPIPE on its second
write, so the pipeline failed and a declared operation was reported as missing.
Each run named a different operation, and the check's own pull request passed
by winning the race. The check now reads the whole header before matching, and
it still refuses a mutating call whose operation the header does not name.
