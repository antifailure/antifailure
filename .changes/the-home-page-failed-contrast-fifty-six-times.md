# fixed

The public site's tertiary grey failed the contrast floor on every page.

axe-core counted 263 colour contrast violations across the six public pages,
56 on the home page alone: the gray-new-50 token measured 3.85:1 on the page
ground, the black opacity utilities below 55% composited to the same failing
greys, and the illustrations carried their own hand-picked greys and greens
that were lighter still. The token is retuned to clear 4.5:1 on every ground
the site paints, tertiary text uses it instead of an alpha, and the
illustration colours are folded onto it. The home page's code panels and the
narrow-width card strip can now be reached and scrolled from the keyboard, the
product page's verdict list contains only list items, and four decorative
icons are hidden from assistive technology.

The header said Docs twice from 1280px up, once in the nav and once with a
book icon beside GitHub, both to the same page. The icon copy is gone.

The documentation site's header said Log in and Sign up where every other page
says Sign in and Start free. It now says what the rest of the site says.

The console's email sign-in field says up front that new accounts start with
GitHub and that the link is for addresses already invited into an
organization. A new address used to get the same "check your mail" sentence
as an invited one, and no mail, with nothing on the page to say why.
