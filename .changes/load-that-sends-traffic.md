# fixed

- A manifest could enable load while CI skipped it, and explicit safe pages
  filtered away the only default route. CI now honors the manifest, sends a
  bounded read-only smoke to literal safe routes when no traffic source exists,
  and reports the request counts. An incomplete load experiment is inconclusive.
- Synthetic smoke requests count missing pages as errors, and load does not
  follow unapproved redirects.
