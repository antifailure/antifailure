# fixed

Every operator mutation the console has ever sent was refused with a 403.

`adminMutate` sent no cross-site token and carried a comment arguing none was
needed, because the operator cookie is SameSite=Strict. The argument is half
right: SameSite is site scoped rather than origin scoped, so a subdomain an
attacker controls is inside it. The server has always disagreed and always
refused, and `admincsrf.test.ts` pins that in three ways. So the portal had a
Suspend button that suspended nothing and a Resume button beside it, and
nothing anywhere was red.

The token comes from `GET /v1/admin/session`. It is fetched once, cached, and
refetched exactly once on the refusal that names the header, which is what
makes a session replaced in another tab recoverable without turning a
permission refusal into a silent second attempt.

`console/lib/admin-money.ts` did not go through that function at all: it called
the product's own `mutate`, which sends the tenant header. Fixing the operator
client alone would have left every billing write broken, and the file had no
importers, so nothing would have noticed.
