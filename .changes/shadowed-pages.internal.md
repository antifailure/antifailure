# added

The site assembly refuses a build in which a redirect in the host config
claims the address of a page the build produced. Six retired product pages
are shadowed that way on purpose, by 301s that must stay because those
addresses were indexed, and nothing compared the redirects against the pages
they answer for. A seventh, added by accident, would have built, deployed,
passed every check on the site, and been reachable by nobody.

The assembly reads the redirects from `www/public/staticwebapp.config.json`
rather than from the merged result, so the file form redirects it generates
for every built page are excluded structurally rather than by pattern. A
shadowed page passes only while it agrees with the host: a `MovedPage` stub
naming the same target the 301 names, and noindex so a crawler does not hold
one page on two addresses.
