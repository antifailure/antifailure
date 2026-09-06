# fixed

A browser offering to darken the site turned the home page's headline white and left the ground under it pale.

Every page of the site, the console and the documentation is authored in one
light appearance, and that is a decision written down beside the theme toggle
it disables. The site said so in the one way that does not hold: `color-scheme:
light` describes the page, and Chrome's automatic dark theme, the setting an
Android reader finds as "Darken websites" and a desktop reader finds behind a
flag, repaints a page that says exactly that. Only `only light` turns it off.

What the repaint does is invert text and flat backgrounds and leave gradients,
images and video untouched, and the home page hero is all four of those in one
place. The label and the headline over the hero film went white, the film and
the fade above it did not move, and the first screen of the site read as white
type on a near white ground. The console declared no color scheme at all and
took the same treatment on every screen. The documentation carried Starlight's
`data-theme="dark"` and the `color-scheme: dark` that comes with it while every
one of its tokens is defined identically for both themes, so it painted a light
page and asked the browser for dark scrollbars and dark form controls around it.

All three now declare `only light`, in the stylesheet and, on the site, in the
meta tag that decides the first paint before the stylesheet arrives.
