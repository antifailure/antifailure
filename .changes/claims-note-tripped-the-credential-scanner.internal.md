# fixed

The claims audit note no longer carries an illustrative Stripe key long enough
to read as a real one.

`scanrepo` matches the `sk_live_` prefix followed by at least sixteen more
characters, which is what separates a credential from prose that mentions one.
The note's placeholder ran twenty characters past the prefix, so a hand written
example tripped a security gate on a landed commit. The note now describes the
value instead of spelling it out. The gate was right to fire and is unchanged.
