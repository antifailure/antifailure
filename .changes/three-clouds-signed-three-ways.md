# changed

The credential path was written once per secret store and needed by six lanes.

AWS Signature Version 4, the Google metadata server plus the service account
JWT exchange, and the Microsoft Entra client credentials flow all lived inside
`ee/engine/secrets`, next to the store that first needed them. None of them is
about reading a secret. Every managed database provider and every runtime that
reaches a cloud API proves who it is the same way, against a different
endpoint, so the next six of those would each have grown their own copy of a
signer where one wrong byte is a 403 that reads like a wrong password. Two
signers that agree today are indistinguishable from one signer until a fix
lands in only one of them.

They now live in `ee/engine/cloudauth`, with the AWS credential chain, both
Google token paths, both Entra token paths and the one bounded HTTP client they
share. The secret sources call it and print exactly the messages they printed
before, including which of the four places AWS credentials were looked for and
which identity a refusal was refused for.

Nothing else about the behaviour moved. The rejected and not-configured
sentinels are the same values rather than two declarations with the same words,
which is what keeps the one-refresh rule working across the boundary, and the
Azure scope is still the vault resource with `/.default` after it, pinned in a
test against the literal string rather than against the constant that derives
it.
