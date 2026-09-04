# fixed

Every mutation in the operator portal was refused before it reached its route.

The console's operator client sent no cross-site request token and carried a
comment arguing none was needed, because the operator cookie is SameSite=Strict.
The control plane disagrees and always has: it refuses every non-GET request to
the tRPC surface that carries a valid operator cookie without a matching
`x-antifailure-admin-csrf` header, and its own suite asserts that three ways.
Nothing in the console ever fetched the token. Both files were correct about
themselves and the product was broken between them, so suspending and resuming an
organization were buttons that could not work.

The token is now fetched once and sent, with a single retry when the transport
refuses it specifically, so a session replaced while a page is open recovers
instead of leaving a button that silently does nothing. A refusal from the route
rather than the transport is not retried, because asking twice gets the same
answer.

The same helper also unwrapped nothing. It answered the tRPC envelope carrying
the type of the value inside it, which the compiler cannot see and no caller had
read: a suspend that promised `{suspended: boolean}` delivered an object whose
`suspended` was undefined. The first caller to read one downloaded a file
containing the word "undefined".
