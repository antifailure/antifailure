# fixed

Two services that needed different values for one variable name both received
the first one, and nothing said which was wrong.

A secret lookup took a name and nothing else. Supabase's published compose file
is the case that found it: its `storage` and `supavisor` services both read
`DATABASE_URL` with a different connection string in each, and both of those are
credentials, so neither can be a literal in a manifest and both have to resolve
through the secrets chain. A manifest could already say where each one was
stored, because `from` renames a lookup, but the resolver merged the two
declarations by the variable's name, kept the first place it saw and dropped the
second without a word. So a twin of that stack would have been silently wrong
rather than visibly broken.

Resolution is keyed by the service as well as the variable now. Declarations of
one name that read from one place are still one lookup, one audit record and one
value shared by every service, so a manifest that needs no per service value
resolves exactly as before. Declarations that read from different places are
separate lookups and each service receives its own answer, which no other
service's lookup reaches.

A manifest can also say so directly with `scope: service`, which stores the value
under the service's name in capitals, two underscores, then the variable, so the
`storage` service's `DATABASE_URL` is `STORAGE__DATABASE_URL`. Every source can
hold a name of that shape, including the enterprise secret stores, with no
adapter change and no change to the order the chain asks in.

A sandbox credential cannot differ between services and cannot be scoped,
because the egress proxy holds one value per credential for the whole
environment and substitutes it whichever service sent the request. That is
refused with AF-SEC-007, naming the variable and the services, rather than
resolved to whichever was seen first. Validation also refuses a scope on a
literal, and refuses two names that would be stored under one scoped name, which
is the same leak read the other way round.
