# security

The workflow identity exchange issued a credential to a suspended organization.

The exchange checked that the GitHub App installation was not suspended, which
answers whether GitHub still says the account is ours. It never asked the other
question: whether we have stopped the customer. A suspended organization was
handed a working fifteen minute engine token and refused later at `/v1/events`,
which points somebody at their continuous integration when the answer is their
billing state.

The refusal now happens before the token is minted, with its own reason,
`organization_suspended`, so a caller can tell it apart from a suspended
installation. The two have different remedies. Work that is already running is
untouched.
