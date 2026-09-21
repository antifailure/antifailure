# fixed

A test fixture standing in for a cloud emulator answered before it had read the
request, which made Go's own HTTP client hand the application a 502 for a
response the emulator had served correctly. It failed on main, on a release
branch, and on a pull request that could not reach it. The fixture now reads
the request first, which was measured rather than assumed.
