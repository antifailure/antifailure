# security

The one advisory the docs carried with no fix, GHSA-ch52-4w7c-c8xp in
http-cache-semantics, now has one. Version 4.3.0 was published hours after the
advisory was accepted in `.npmaudit.yaml`, so the docs take it and the
acceptance is removed. Every lockfile in the repository audits clean again,
with nothing accepted.
