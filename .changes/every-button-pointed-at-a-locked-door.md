# changed

The primary action on antifailure.dev is the free path now, everywhere the
product is pitched. It was "Request access", which leads to an invitation wall
that only a handful of GitHub accounts can pass, so the site described an
open-source engine and then pointed every visitor at a door they cannot open.
The engine is MIT licensed and installs with one command, so that is what the
filled button does: the home hero, the closing panel on every page, the default
hero on every inner page, the solutions heroes and the header now lead with the
quickstart and offer hosted access beside it. The hero also carries the install
command itself, which the closing panel already did.

The hero's two sentences swapped order for the same reason. It opened with the
restriction and closed with what a visitor can have; it now says what they can
have first.

The `Docs` link in the header of every page was a `next/link`. The
documentation is built by Astro and merged into the published site afterwards,
so the app router does not own `/docs`: it prefetched a payload that does not
exist and answered the click with a client side navigation to nothing. The
header already had the helper that makes this distinction and this one link was
not using it, which is the third time this exact defect has been found on this
site.
