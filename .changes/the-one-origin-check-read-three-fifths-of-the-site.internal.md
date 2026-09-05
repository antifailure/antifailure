# fixed

The check that every published file names one origin had never opened the
documentation.

It asserted, in its own words, that no built file spells the site any way but
the canonical origin. It read `www/out`, which is 307 of the published site's
499 served files. The other 192 are the documentation, the installer and the
JSON schemas, which are copied into the tree by `tools/site/assemble.sh` and
which that check had never seen. Roughly three fifths of the site carried a
claim made about all of it, and the deploy step that does read the documentation
resolves links and has no opinion about which host a page names. A documentation
page could have sent a reader to an address that is not the canonical one and
every stage would have stayed green.

Nothing was wrong in the unread part on the day this moved. That is the reason
to move the check rather than a reason to leave it: a gate whose claim is wider
than its reach has not been lucky, it has never been asked the question.

The assertion now lives in `tools/site/assemble.sh`, which is the only step that
holds both builds plus the files neither build produces, and it asserts its own
reach as well as its content. It requires the canonical origin to appear both
under the documentation and outside it, so a walk that narrows back to one half
of the site fails by name instead of passing over an empty set. The wrong
spellings are derived from the canonical one rather than listed, because the
list it used to carry was a survey of the spellings somebody had thought of and
the one that caused trouble arrived after it was written.
