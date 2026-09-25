# fixed

`curl -fsSL https://antifailure.dev/install.sh | sh` told people there is no
release when the truth was that it could not ask. It resolved `latest` through
`api.github.com`, which allows an unauthenticated caller sixty requests an hour
for each IP address and answers 403 afterwards. `curl -f` turns that 403 into an
empty string, the `sed | head` pipeline discarded the exit status, and the empty
version fell into `no release was found; set AF_VERSION to install a specific
one`. Sixty an hour is shared by everybody behind one address, so a corporate
NAT, a cloud network or a shared CI runner reaches it without doing anything
unusual, and this is the first command a stranger runs. Our own CI reached it:
the job that installs the way a customer's workflow does invokes the installer
eleven times, and the first invocation was told the product has no releases.

`latest` now comes from the redirect on
`github.com/antifailure/antifailure/releases/latest`, which lands on the tag
GitHub marks as the latest release. That is the website rather than the API, it
needs no token and it has no budget for each address, so the case that broke
cannot arise. Serving the version from `antifailure.dev` was considered and
refused: the site deploys when a merge lands on main and a release is published
when a tag is pushed, so a version file written at site build time would be
stale for every release until an unrelated merge rebuilt it, and installing the
wrong release quietly is worse than saying the question could not be answered.

Answers that used to arrive as one sentence now arrive as what they are. Nothing
answering, an address that has asked GitHub for too much, a repository that does
not exist or is private, a repository that has published no release, a redirect
naming something that is not a tag, and a redirect to somewhere that is not a
release at all, which is what a proxy or a sign-in portal in front of a network
answers with, each say what happened and what to do about it. The rate limit
names `AF_VERSION` because that genuinely is the way through, and `AF_VERSION` is
documented now, which it was not.
The same collapse is fixed for both downloads. A release with no build for this
platform is no longer reported as `could not download`, a `checksums.txt` that a
network dropped is no longer reported as one that was never published, and a
version nobody published, which is what a mistyped `AF_VERSION` is, is no longer
reported as a platform with no build: the release page is asked which of the two
a 404 on an asset means, and the reader is pointed either at the builds that
release does carry or at the releases that exist. Both implementations are fixed,
`curl` and `wget` alike.

A machine whose only fetcher is BusyBox wget, which is what Alpine ships and what
this script's own header names as a place somebody pipes it into, keeps working.
That wget has no option that reports a redirect and refuses every flag GNU wget
reads one with, and the lookup this replaced worked there, so the version is read
instead out of the newest release's own `checksums.txt`, which github.com serves
under `releases/latest/download` and which names the version in every archive it
lists. That second way of asking is also what answers if the redirect ever stops
being a redirect.

None of this was tested and none of it could have been. Every session in
`tools/installsh` set `AF_VERSION`, so the resolution never ran, and the stub
`curl` could only exit 22, which cannot express a status at all. The stub is
gone: the tests now run the real `curl` and the real `wget` against a local
server that answers real statuses, and each arm asserts both the sentence it
should produce and the absence of the sentence it used to produce.
