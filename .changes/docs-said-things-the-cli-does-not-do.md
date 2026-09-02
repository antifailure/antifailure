# fixed

Four places where the documentation described a command the product does not
have.

The operations runbook told an on-call engineer, in bold, in a list of things
not to do during an incident, not to run `af down --all`. There is no `--all`
flag; `af down` takes `--branch` and nothing else. The warning was real and the
command was not: the thing that removes every environment on a machine is
`af env prune --older-than 0`, which is what the page says now, along with the
`--dry-run` that shows what would go and the `af env reap` that removes only
what has already expired.

The provider keys guide showed a nine line `af provider list` session and every
line of it was wrong. Rendered by calling the real table with the real column
definitions and the real cell strings, the headers are upper case, the key is
masked with eight ASCII asterisks, the two money columns are right aligned, and
a provider with no budget reports `not tracked`. The page showed title case
headers, bullet characters, left alignment, and an em dash. The code carries a
comment saying bullets were rejected because this output is pasted into pull
request comments, and another saying a dash was rejected because it reads as
unlimited and means the opposite. The page was showing both rejected forms, on
the screen somebody reads before typing their own API key.

The model keys guide invented the message that fires when an exported variable
shadows the key you just stored, and dropped the clause naming where the other
key is set, which is the only part of it that tells you what to do. It also
never showed the message you get when nothing shadows it, which is the common
case.

Two links carried another page's exact title. The quickstart's last section
offered "An environment per pull request" and went to the GitHub reference,
while the page with that title sits next to it in Getting started and the
quickstart never linked to it at all. A page about load offered "invariants"
and went to the manifest schema rather than to the invariants guide.
