# changed

`af init` stopped one step short of a working check. It wrote the manifest,
and the pull request integration then needed a workflow file the reader had to
find in the documentation and copy by hand, into a path they had to get right,
before anything ran. Most people never got that far, and the ones who did were
copying a file whose only job is to call another one.

It writes `.github/workflows/antifailure.yml` now, when the checkout has a
`github.com` remote, and adds `github: {mode: actions, comment: true,
fork_policy: label}` to the manifest so the settings the file depends on are
in a line somebody can read. A file that is already there and differs is left
alone and said to be. A checkout on some other forge gets one sentence naming
`af github init`, which writes the same file into a project that already has a
manifest and prints, by name, the secrets the check can use and the one
repository variable a hosted control plane needs.

Two refusals went with it. A port nothing in the repository named was a
question with no default, so `af init --non-interactive` refused with
AF-DET-004 on the median containerised repository, a Dockerfile with no
`EXPOSE`. Every question carries a default now, the language's own port and
its conventional start command, listed under Assumed. And a run with no
terminal used to refuse with AF-MAN-004 and tell the reader to pass
`--non-interactive`, which in a CI job was the only thing they could have done
anyway; it takes the defaults and says so instead, and AF-MAN-004 is gone.

When the manifest names a production database and this shell holds it, `af
init` also writes `masking.yaml` from the schema, and says which of the two
conditions was missing when it could not.
