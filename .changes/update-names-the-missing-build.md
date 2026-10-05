# fixed

`af update` on a platform the newest release has no build for refused with
"no valid SHA256 checksum names this archive", which reads like a tampered
download. Every Windows user saw it, because the latest release when Windows
support landed shipped no Windows archive. It now says what happened: release
v1.9.0 publishes no build for windows/amd64, nothing was downloaded, and the
installation is unchanged. A checksum that is named but is not a checksum is
still refused in the old words, because that one is a damaged release.
