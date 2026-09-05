# added

The launch film on the home page, and a gate that can see a video looping.

Eighty five seconds, under a heading that says what it shows: a copy of
production the same size and shape and load with every real name replaced, the
change running there first on every pull request whether a person or an agent
wrote it, a migration caught holding an exclusive lock on 48,201,338 rows and
taking `GET /orders` from a 240ms p99 to 4,730ms, and the copy deleting itself
afterwards. Nothing reached production, which is the line the film ends the
check on.

It does not autoplay and it does not loop. `tools/motioncheck` exists to refuse
a piece of the interface that animates forever while the reader does nothing,
and its rule has no carve out for a real event, so it has none for a film. This
one plays once when the section is scrolled to, settles, and stops, which is
what the twin figure on the same page already does, and it does not start at all
under `prefers-reduced-motion`.

**`motioncheck` could not have seen it either way, and now it can.** Everything
that gate reads is CSS, from a stylesheet or from a style attribute, and
`<video autoplay loop>` is neither. A page could carry two of them and the gate
would report "0 animations that never stop" over the file they were in. It
reads the built HTML for a video that carries both attributes, names the source
file so an exemption can be copied from the failure, and takes each attribute as
a whole attribute rather than as a substring, because `loop` is inside
`loop-demo.mp4` and a rule that refuses a video which does not loop is a rule
somebody deletes.

The film is 9.8 MB rather than the 51 MB it was cut at, and nothing downloads
until somebody scrolls to it. `preload` is none and playback is started by the
viewport observer, so a visitor who never reaches the section pays nothing for
it. The site is served by a Static Web App whose plan allows 250 MB in one
environment and 100 GB of bandwidth a month with no overage available, which is
the arithmetic that decides this rather than a preference about page weight.

Three things that were wrong before this landed, all of them found by looking at
the render rather than the source. The observer could not START playback, only
resume it: its flag began as "playing", so the first time the section came into
view it did nothing, and the `autoplay` attribute was what made the section look
like it worked. There was no play control at all once autoplay was removed, only
a circular arrow labelled restart, so anybody with reduced motion on was looking
at a still image with no way to play it that said so. And the three video
controls sat at the bottom right, where the film burns its captions, so on a
phone "Last year, developers" ran underneath them. They are 44 pixels now rather
than 40, which is the tap target floor this project sets and every video control
on the site was under it.
