# fixed

constcheck no longer reads `www/out`, so a local static export cannot produce
findings about prose the source has already corrected.

The walker already skipped `node_modules`, `.next`, `dist` and `.astro` for
this reason and simply did not know about Next's static export directory. On a
machine where `next build` had run, it reported two miscounts citing
`www/out/product.md` and `www/out/product/migrations.md` for sentences the TSX
no longer contains. CI never saw them because the job that runs constcheck does
not build the site, which is why this survived: the false finding was visible
only to a person, and only to one who had built www.
