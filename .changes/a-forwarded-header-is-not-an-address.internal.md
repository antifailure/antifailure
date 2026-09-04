# fixed

A caller behind two proxies could turn an operator route into a 500.

`x-forwarded-for` is a comma separated list whose entries may carry ports, and
an IPv6 one may be bracketed. Postgres refuses all of those on an `inet`
column. `server.ts` already had the parsing and it was private to that file, so
the second file that wrote a caller's address wrote the raw header instead. It
is now `clientaddress.ts`, imported by both.
