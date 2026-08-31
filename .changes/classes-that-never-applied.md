# fixed

The site navigation did not mark the page you were on. On the pricing page the
word Pricing rendered exactly like Docs and Writing beside it, and the same on
every other page with a plain nav link. It has been that way for as long as the
header has existed, and it went unreported because moving the mouse over a link
darkened it correctly, so the navigation looked right the moment anyone touched
it and was wrong only at rest.

Five more of the same kind are fixed with it. A live environment card in the
opening animation rendered its live state and its idle state in the same grey.
Two pills that mark a problem got their red text and their red outline but kept
a neutral grey fill, so they sat next to a neutral pill looking almost the same
as it. A card that is meant to sit back rendered at full strength. One of those
pills also carried a red used nowhere else on the site and now uses the shared
one.

All six had the same cause. A colour written on an element does not replace a
colour the component already set: both land on the element and the browser picks
between them by the order the stylesheet happens to be written in, which has
nothing to do with which one the author meant. Each is now written as a choice
between colours rather than one laid over another, so there is nothing to pick
between.

A new check reads the built pages and refuses a class that another class on the
same element beats, so the next one fails on the pull request that adds it
rather than shipping and going unnoticed for a year.
