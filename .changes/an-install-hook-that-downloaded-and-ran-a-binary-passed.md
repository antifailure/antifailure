# added

The dependency checks read the versions and not the shape of the change. A
pull request could add a postinstall hook, point it at a script it downloaded
with curl and piped into a shell, or move a dependency's source from the
registry to a git URL, and the rehearsal classified the file as a dependency
change and looked no further. The advisory scan that runs beside it answers a
different question, whether a version is known-vulnerable, and none of these is
a version.

There is a supply-chain family now, on the dependency surface. It reads the
lines the change ADDS to a manifest or a lockfile, never the branch's absolute
dependency tree, so an install hook a lockfile already carried is not counted
against this change and one this change adds is. It catches three shapes the
advisory scan does not: a lifecycle install hook added to a package manifest,
an install step that downloads a payload and runs it, and a dependency whose
source moved off the registry to a version-control URL. The last excludes a
repository or homepage field, whose value is a URL by design, and requires the
scheme to begin a dependency value rather than appear in prose, so a metadata
line is not the false positive that gets the rule switched off.

The family refuses a change on policy grounds, so its findings carry the
policy-denial exit code, and a project gates each through
`security.supply_chain.install_script_added`,
`security.supply_chain.binary_download_in_install` and
`security.supply_chain.registry_source_changed`. Each defaults to warn, because
a dependency change is noisy and a check that reddens every bump is one a team
mutes, and a project raises it to fail once its dependency changes are clean.
A finding names the file and the risk and never prints the line.
