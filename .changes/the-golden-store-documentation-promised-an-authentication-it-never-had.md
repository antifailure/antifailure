# security

The documentation said a pulled golden was checked to be yours. It is checked
against an accident.

A pull reads the attestation beside the dump, compares the project identity
recorded in it, and refuses a version made for another project. That check was
written for an accidental collision, and it is measured against exactly that: a
project with no masking.yaml shared a golden pool with every other one, and
nova-shop came up holding acme-billing's customers. The pages said more than
that. `concepts/goldens` said a pulled golden was not trusted to be yours,
"signed along with everything else", and `providers/stores` said the signed
statement was what made a dump a golden rather than a file. Both read as an
authentication of the publisher, and there is none: the pull compares the
provenance field and never checks the signature, and the signature would not
answer that question anyway, because the key is generated for each signature and
travels inside the document. It proves the document was not changed after
signing, not who signed it.

So the pages now say what holds. What protects the data in a pulled golden is
the verification scan running again on the machine that pulled it, against the
database that actually arrived. What decides who may publish is the store's own
access control, which makes the store credentials and the bucket policy the
trust boundary, and `providers/stores` says that in those words. The same
correction is in the comments that carried the claim in the code, on
`verify.Attestation`, on the pull's provenance check and on the `golden.Store`
alias. No behaviour changed.
