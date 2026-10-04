# security

The console, the website, the docs and the Next.js example were built on a
release of Next.js with a critical advisory, GHSA-vcvr-r3jv-pc5j, for five
days. The daily vulnerability scan reported it from 2026-09-29 onwards, and
issue #612 said scanning had stopped protecting the repository, but the scan
only reports. Nothing it found reached a lockfile.

Next.js moves to 16.3.6 in all four. The same pass takes the fixed releases of
undici and devalue in the docs, ip-address and fast-uri in the open source
web workspace, and DOMPurify in the website, and folds in the open dependency
updates that were waiting on a regenerated third party notices file.

One advisory is accepted rather than fixed, because no fixed release exists:
http-cache-semantics through 4.2.0, which the docs reach only through Astro's
build time cache for remote images. The docs are built to static files, so no
request is ever served through that cache. The reason and an expiry are in
`.npmaudit.yaml`.
