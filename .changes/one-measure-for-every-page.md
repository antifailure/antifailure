# fixed

A tinted section on the product pages painted its background 10% short of the
page on each side and then ran its own text flush against that edge, so the
green band stopped exactly where the heading, the verdict chips and the
paragraph began, with several hundred pixels of empty colour left over on the
other side. On /product/architecture at 1920 the band ran from 192 to 1728 and
the text started at 192 with nothing between it and the boundary.

The cause was a shared shell that was not shared. `PageShell` took an `inset`
prop; the eight product pages passed it and the six legal pages, the four
solutions pages and pricing did not. It put a 10% margin on the whole page and
then reached back into the shared container with `!max-w-none !px-0` to remove
the measure and the gutter that container exists to provide. A transparent
section survives that because the missing gutter is invisible. A tinted one
does not, because the tint is what makes the gutter visible.

The two measures were the same number where it mattered: a 10% margin at 1920
leaves 1536, and `max-w-[1600px] px-8` at 1920 also leaves 1536. So the prop
bought nothing at the wide end and only narrowed the page below it. It is gone,
every auxiliary page takes the one measure the shell owns, and the tinted band
is full bleed with the standard gutter at every width, the same as the
homepage. The band's colour is the existing `--color-sage` token rather than
the hex that had been typed into the section by hand.
