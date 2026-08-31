# fixed

Two `prefers-reduced-motion` blocks in the marketing site's stylesheet named
fifteen selectors between them and eleven of those matched no element on any
page. The first block, which was the whole of the sign-in screen's motion
guard, matched nothing at all. Accessibility work that has silently stopped
running reads exactly like accessibility work that still runs, which is what
makes this worth more than the bytes it saves: the file said the site respected
a reduced-motion preference in places where there was nothing left to respect
it for.

The guards are now the four selectors that still reach an element.

# changed

Eighteen classes and ten keyframes in the same stylesheet rendered nowhere.
They were leftovers rather than anybody's next step, and the history says so
rather than the file: nine of them lost their consumer in commits whose subject
lines are about deletion, one of them "delete the half of the site nothing
renders". The four that never had a consumer at all are led by the sign-in
screen's animated bars, and that screen shipped with a honeycomb behind it
instead, so a different treatment was chosen and built rather than deferred.
The background-pattern family they belong to has one survivor. Removing them is
recoverable from history; leaving them meant the next person reading the file
could not tell which half of it was live.

`caret-live` was the inverse case, a class name applied to an element in the
caret component and never styled anywhere, since the day the site was first
committed.

One keyframe that looked equally dead is not, and it is worth naming because
the search that found the others could not see it: `wt-sheen` is reached from
an inline style in `components/IdeSection.tsx`, so nothing in the stylesheet
mentions it. It plays once and stops, and it stays.
