# Merging the held-back motion commits into the landed globals.css

`w-docsgraph` landed one of its three commits this release. `b2b42da`, the docs
graph commit, is on `main`. The two that touch `www/` were deliberately held
back and will need merging next release:

    727e19a  www: the seven loops that were still running
    b3cfa10  www: the hero films play once and rest, and the rotation stops

This note is the analysis of that merge, written while it was fresh. It was done
against `ff89307`, before `w-uipass` landed, and then rechecked against `main` at
`499d28b` with `w-uipass` in it. Both states are recorded, because the second one
is much easier than the first and it would be easy to redo the hard version by
mistake.

## Why the conflict looked unresolvable

Merging `w-docsgraph` onto `w-uipass` conflicts in `www/app/globals.css` in four
regions. Every instinct says pick a side, and every choice of side is wrong.

Neither branch adds anything to any of those regions. Every rule on both sides of
every marker is already in the merge base. Both branches only DELETE, and they
delete different subsets, so git has no common line to anchor on and presents
each side's surviving remainder as though the two were alternatives. They are
not alternatives. They are two different subtractions from the same list.

    w-uipass deletes the rules nothing references:
      mesh-grid, sage-grid, carbon, scanlines, wt-scan, auth-rise, auth-glint,
      auth-bar, ide-dots, ide-grid, film-scanline, film-log, film-log-slow,
      film-dash

    727e19a deletes the animations that loop forever:
      wt-scan, auth-glint, film-scan and its .hero-scan, film-scanline,
      film-log, film-log-slow, film-dash

They agree on everything they both name and contradict each other nowhere. So
the resolution is the union of the two deletion sets, and taking either side
alone silently keeps rules the other proved should go.

## Why it is much easier now

`w-uipass` has landed. On `main` at `499d28b` every rule in that union is already
gone except one:

    globals.css:128   animation: film-scan 7s linear infinite;

`film-scan` and its `.hero-scan` are the only part of `727e19a`'s stylesheet
deletion still outstanding, because they are the one rule in its set that
`w-uipass` could not touch: `w-uipass` removed only rules nothing referenced, and
this one IS referenced, at `HeroFilm.tsx:47`. `727e19a` removes that div in the
same commit, which is what makes the rule dead and removable.

So next release the stylesheet half of `727e19a` is close to a no-op. Rebase it
and expect almost all of its `globals.css` hunk to drop out as already applied.
What must survive the rebase is the `film-scan` keyframes, the `.hero-scan`
rule, and the `HeroFilm.tsx` line that renders it, together. Removing the rule
without the div leaves an element with no animation; removing the div without the
rule leaves the last infinite loop in the file with nothing to apply it to.

## The reduced-motion block, which must not be taken from the branch

The merge base held a fifteen selector class list with `animation: none
!important`. `727e19a` prunes that list to eight, because it deleted five of the
rules it named. `w-uipass` replaced the list wholesale, and that replacement is
what landed:

    @media (prefers-reduced-motion: reduce) {
      *, *::before, *::after {
        animation-duration: 0.001ms !important;
        animation-iteration-count: 1 !important;
        transition-duration: 0.001ms !important;
      }
    }

Take the landed form and drop the branch's, on the rebase, for two reasons that
are separate.

A list goes stale and this one already had. Eleven of its fifteen selectors had
stopped matching any element, including both selectors of the sign-in screen's
guard, so that block was respecting a reduced-motion preference for nothing at
all. A universal selector covers every `film-*` rule without naming one, so it
strictly subsumes what the branch's list is for.

And `none` is the wrong verb. Anything whose resting state is the END of its
animation never arrives under `animation: none` and sits invisible or half
drawn, which is exactly how the SVG scenes on the product pages draw
themselves. Collapsing the duration to a rounding error runs every animation and
lets it finish, just faster than a frame.

## How to check the result, which is not by reading it

Read the built stylesheet, not the source. The interesting properties only become
true in the merged tree, so neither commit's author could see them from their own
branch, and a source diff shows a plausible-looking file either way.

    cd www && npm ci && npm run build
    grep -c infinite out/_next/static/chunks/*.css      # expect 0

`npm ci` first is not optional: a stale Next 15 install against a lockfile
pinning 16 behaves differently and has already invalidated one round of
verification here.

Three more that catch what a diff does not:

- No `@keyframes` left without a consumer, and no class defined in the
  stylesheet left without a reference in the components. Compute both against
  the merged tree.
- `wt-sheen` must survive. It is reached only from an inline style in
  `IdeSection.tsx`, so any sweep that reads only the stylesheet will call it
  dead and delete a live animation. Grep for the name rather than trusting a
  line number; this one had already moved between being written down here and
  being checked.
- No deleted class still referenced in the built HTML.

The count reaching zero in the shipped CSS is worth more than any amount of
reading the diff, because it is the artifact the browser loads.
