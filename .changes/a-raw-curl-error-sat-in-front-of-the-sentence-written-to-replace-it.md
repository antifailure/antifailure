# fixed

A refused download printed `curl: (56) The requested URL returned error: 404` at
the reader immediately before the sentence the installer composes to explain what
happened, so the one line install answered a mistyped `AF_VERSION`, a platform a
release does not carry, and a rate limited archive with a raw tool error stacked
on top of the careful message written to replace exactly that kind of noise.

`curl` is invoked with `-sS`, which is silent but shows errors, and of the three
places `install.sh` fetches something only this one failed to discard the tool's
own stderr. The number in that line is curl's error code rather than the HTTP
status, and the same 404 surfaces as 22 or as 56 depending on how the request
failed, so a reader who takes it for a status reads a different number each time
and neither one is the status `why_not` goes on to report from the same URL.
Nothing is lost by silencing it, because `why_not` asks the URL again and names
the status itself, which is why the other two call sites already silenced it.

The tests could not see it. Three of them drive an archive download to a refusal,
and each asserts that the sentence it cares about is present and that the sentence
it replaced is absent, which says nothing about a line added beside them. Every
invocation of the installer in `tools/installsh` now also asserts that neither
fetcher spoke in its own voice, alongside the check for raw shell errors that was
already made on every successful install and for the same reason: a leak of this
shape appears on whichever path forgot to silence the tool, so the check belongs
on all of them rather than in one test.
