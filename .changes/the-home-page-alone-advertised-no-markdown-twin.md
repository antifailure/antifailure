# fixed

Every indexable page on antifailure.dev advertises a machine-readable Markdown
twin in its head, a `rel="alternate" type="text/markdown"` link an assistant
follows to read the source instead of parsing the rendered page. Every page
except the one most likely to be discovered first. The home page exported no
page metadata at all, so it inherited only the site-wide canonical and, alone
among the indexable pages, pointed at no twin. The build wrote `/index.md` and
the host answered 200 for it, and an agent reading the home page's metadata to
find it never learned it was there.

The home page routes through the same registry every other page uses now, so it
carries the twin link like the rest. A second fault sat behind the first: the
metadata builder produced the root's twin address as the origin with `.md`
stuck on the end, `https://antifailure.dev.md`, a host that does not resolve,
because the root is the one page whose url is the bare origin rather than a
path. The root twin is `https://antifailure.dev/index.md` now, and every other
page's is unchanged.

The SEO check could not have caught either: it walked only the twins a page
already advertised, so a page advertising none was invisible to it. It now
asserts that every indexable page's head advertises its own twin at the address
the build actually wrote, which fails on the home page as it stood and passes
once the link is there.
