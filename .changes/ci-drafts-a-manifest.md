# added

A repository with only the workflow file got no check. `af ci` and `af change`
needed `antifailure.yaml`, so the first pull request after installing the
workflow, or the GitHub App, ended in AF-MAN-001 and a red step. The workflow
being something nobody has to write was worth nothing while the next thing
they met was a file they had to write.

With no manifest, both commands now draft one in memory the way `af init`
would, take every default, and run on it. The report opens with a bold line
saying so and naming `af init` as the command that makes the file theirs. A
service nothing could be guessed for is left out of the draft and named,
rather than the whole run refusing. When nothing at all can be drafted, `af
ci` writes a skipped run that says why and exits zero, because nothing was
learned about the change.

The draft is never written to disk. A file that appears in a checkout because
a CI job ran is a file nobody committed and nobody can explain.

`af change` also writes two more keys to `GITHUB_OUTPUT`: `source_url_env`,
the variable naming production, and `secrets`, every variable the manifest
reads a credential from. Names only. The action exports exactly those out of
the caller's secrets, which is what lets a reusable workflow pass the whole
secret set without the customer naming each one.
