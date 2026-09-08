# security

This repository's own manifest carried a private key, and the check named "no
credentials in the tree" passed over it.

`antifailure.yaml` declared `AF_GITHUB_APP_PRIVATE_KEY` with `value:` set to a
2048 bit RSA key, base64 encoded rather than PEM. The comment beside it gave
the reason for the encoding: the manifest validator refuses a literal beginning
with BEGIN, and base64 does not begin with BEGIN. The published schema says
what a manifest is in as many words, that it declares the name and where the
value comes from and never the value itself, and the file held up as the
example of that was the one breaking it.

The detector behind the credential gate had a matching hole, and it was a
deliberate decision taken one step too far. A bare PEM is not evidence of
Google, so reporting one as a service account key would send somebody to rotate
a credential they do not have. That was implemented as reporting nothing at
all, so any private key with no cloud marker beside it was invisible to the
gate, to the proxy tripwire and to the manifest validator. It now reports a
private key under its own name, owning no provider, and Google's own marker
still promotes the finding to the more specific one. A second pass decodes
base64 before deciding, so the encoding that walked past the validator does not
walk past this.

The key itself is supplied rather than written down. A manifest declaring a
webhook path for GitHub is rehearsing a GitHub App, so the engine now generates
`GITHUB_APP_PRIVATE_KEY` for the life of the environment and offers it to the
chain the way it already offers the webhook signing secrets. Nothing is
configured outside the manifest and `af up` needs no setup it did not need
before.
