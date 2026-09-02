# fixed

The release runbook told maintainers to cut a signed tag, and no signed tag has
ever been cut.

Both runbooks said `git tag -s`. There is no signing key on the machine that
cuts releases, no `user.signingkey`, and no `tag.gpgsign`, so the command fails
where it is not already impossible, and `git verify-tag` on v0.1.0 or v0.1.1
reports no signature found. A reader who trusted the page and checked would
find the check failing and reasonably wonder what else on the page was wishful.

The runbooks now say `git tag -a`, which is what happens, and say plainly that
the tag carries no signature and that `checksums.txt` and the bill of materials
do. What is signed did not change: cosign keyless signing by the publish job,
verifiable against the workflow identity, is the guarantee and always was. A
maintainer who wants signed tags as well now has the four setup steps written
down, stated as something they choose to do rather than something already true.
