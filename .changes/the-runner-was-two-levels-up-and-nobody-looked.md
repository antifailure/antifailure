# fixed

`af runner install` now finds the runner from anywhere inside a checkout,
rather than only from its root.

It searched the working directory and its parent, which assumed the working
directory was the root of the checkout. That is usually true, so it held until
something ran from a subdirectory. Run from `examples/go-api`, it searched
`examples/go-api/runner` and `examples/runner`, found neither, and reported
that no runner source existed while standing two levels below a checkout that
had one. The remedy it printed told the reader to install a runner they
already had.

The search now climbs to the directory holding `.git` and stops there. It
stops at the checkout rather than the filesystem root because a runner outside
it belongs to something else, and copying that one would succeed, which is the
worse failure: the wrong runner stays invisible until a test will not run.
Outside a checkout the two nearest directories are searched, which is the pair
this looked in before, so nothing that already worked has changed.
