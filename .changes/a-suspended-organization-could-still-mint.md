# security

A suspended organization could still be issued a callback credential.

The suspension was read at `/v1/events` and nowhere else, so the control plane
issued a working credential to an organization it had stopped and then refused
the report that credential was minted for. Nothing crossed a tenant boundary
and no events were accepted, which is why this went unnoticed: the outcome was
correct and only the explanation was wrong. A customer saw the credential
succeed and ingestion fail, and went looking at their continuous integration
when the answer was their billing state.

The refusal now happens where the credential is minted, and it names the
suspension and the recorded reason. Checks that are already running are
untouched, which is the same promise the kill switch makes everywhere else.
