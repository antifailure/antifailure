# security

An air gapped installation refuses to pull an emulator or companion image from
an external registry. Cached images still work, and an image inspection failure
is reported instead of being treated as an absent image that should be pulled.
