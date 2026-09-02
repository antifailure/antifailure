# added

Version 1.0. The commitment is written down surface by surface in
[what is stable](/docs/reference/stability/) rather than made as a blanket
claim: a manifest declaring `version: 1`, the commands with their flags and
exit codes, the documented `--output json` fields, the provider interfaces and
the error codes. Breaking any of those costs a major version. The page also
names what is deliberately not covered, which is the half worth reading before
building against something: the Helm chart's values, the Terraform variables,
the control plane's internal HTTP API, the event type set, and the lint rule
names.

Release notes are written by hand now. `CHANGELOG.md` holds one section per
tag and `tools/relnotes` emits the section for the tag being published, so the
first thing a reader sees is prose about what changed rather than the list of
merged pull requests the workflow used to generate.

That command also owns the verification preamble, and that is the part worth
knowing about. The release action tries `body_path` first and falls back to
`body` only when the path cannot be read, so the moment a notes file read
successfully the preamble carrying the cosign `verify-blob` command would have
been dropped from every release note, silently, with the step still green. One
emitter means there is no second copy to lose.

The gate refuses a section that is empty as well as one that is missing,
because a heading with nothing under it reads as finished in a diff. It runs on
every pull request, since at tag time the only remedy is deleting a tag people
may already have fetched.

`tools/tagsync` refuses a version pin that names a tag nobody has published.
Most version strings are read from a released tree and should name the release
being cut, but the Terraform `image_tag` defaults are not:
`azurerm_container_app_job.maintenance` reads the image with no
`ignore_changes`, unlike the bootstrap job and the application beside it, so an
apply from `main` takes the value and a default naming an unpublished tag fails
the apply on the stack that runs the product. Bumping those defaults after the
tag is now a gate rather than a sentence somebody has to remember once a
release.
