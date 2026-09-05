# added

Every pull request gets a check from one file of about thirty lines, and the
product writes that file. `af init` writes it when the checkout has a GitHub
remote, `af github init` writes it into a project that already has a manifest,
and installing the GitHub App opens a pull request adding it. The file's one
job calls a reusable workflow, `.github/workflows/check.yml`, which calls the
action, `antifailure/antifailure@v1`, and a moving `v1` tag follows every final
release so the line never needs editing.

The documented path was a 505 line workflow that every user copied into their
repository and then edited, and three things were wrong with it that no gate
could see. The runner was never installed by it, so the agents could not drive
a browser in CI and every workflow that needed one came back unverified on the
page that taught the integration. The production database secret was never
mapped: the manifest names the variable under `database.source_url_env`, the
file did not know the name, and so the check ran on an empty schema for anyone
who followed the instructions and reported nothing about it. And a file that
long is a file every copy diverges in, so a fix to the template reached
nobody who had already copied it.

The action installs the runner when the command needs a browser. The reusable
workflow sees the caller's secrets through `secrets: inherit`, hands them to
the action as one JSON value, and the action exports only the variables `af
change` says the manifest reads, by name, then drops the JSON before the
engine starts. So the production secret reaches the check under whatever name
the manifest chose, with no line in the customer's file naming it, and the
report says at the top when it ran on an empty database instead.

A repository with no manifest is checked too: `af ci` drafts one from the
repository and the comment says so in its first lines, with `af init` as what
makes it yours.
