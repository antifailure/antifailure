# fixed

The breadcrumb trail failed the contrast floor on every page of the site that
renders one.

`text-gray-new-50` is `#797d86`, and at 13px that measures 3.85:1 on the paper
ground, 4.13:1 on white and 3.55:1 on the sage bands. Three grounds, three
failures, on the trail that sits above every blog post, every product page and
every solutions page. It is also the first thing a procurement accessibility
review looks at, because it is at the top of the page and it is text.

`gray-new-40` is the next token up and clears 4.5:1 on all three: 5.53, 5.93 and
5.10. Nothing outside the scale was invented; the scale already held a passing
grey one step away, which is why this is a one token change rather than a new
colour.

The `/` separator moved from `gray-new-80` to `gray-new-50`, 1.51:1 to 3.85:1.
That one is stated rather than implied: it does NOT reach 4.5:1, and it does not
need to. It is `aria-hidden`, it carries no information, and darkening it to the
labels' own weight would make a separator compete with the words either side of
it. 1.51:1 was a separator nobody could see; 3.85:1 is one that reads and still
recedes.

Measured in a browser on the rendered pages rather than from the token file, at
390 and 1440, across `/blog/<post>`, `/solutions/saas`, `/product/twins` and
`/pricing`: every link 5.53:1, every current page crumb 11.97:1, every separator
3.85:1.
