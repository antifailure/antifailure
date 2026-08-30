---
title: Single sign-on
description: SAML 2.0 and OIDC per organisation, with enforcement and a way back in.
sidebar:
  order: 2
---

Members sign in through your identity provider instead of through GitHub. SAML
2.0 and OpenID Connect are both supported, per organisation, and you can require
one so that GitHub sign-in stops being a way into your tenant.

This is an enterprise feature. It lives in `ee/web/sso`, under the Antifailure
Enterprise License, and the community build does not contain it.

## What you need before you start

An organisation, an owner account in it, and control of the DNS for the email
domains your people use. You cannot claim a domain by typing it: you prove you
control it with a TXT record. Without that rule, an organisation that runs its
own identity provider could assert `someone@gmail.com` and be linked to whoever
holds that account here.

## Connecting SAML

Your provider needs two URLs from us, and they carry a per-connection
identifier rather than your organisation's name:

```
Entity ID / Audience   https://<your-control-plane>/sso/saml/<handle>/metadata
Reply URL / ACS        https://<your-control-plane>/sso/saml/<handle>/acs
```

Both appear in the service provider metadata document, which most providers can
import directly:

```sh
curl https://<your-control-plane>/sso/saml/<handle>/metadata
```

From your provider you need its entity ID, its HTTP-Redirect single sign-on URL,
and its signing certificate. Paste its metadata document and all three are read
out of it. If it publishes two certificates because it is mid-rotation, both are
kept: an implementation that holds one certificate has a planned outage every
time the provider rotates.

Your provider must send an email address, either as the NameID with the
`emailAddress` format or as a claim. The claim names Entra ID, Okta, Google
Workspace and the generic `email` form are all recognised. An assertion carrying
no email address is refused, with a message saying so, because the address is
what links a person to their account here.

### What an assertion has to satisfy

Every one of these is checked, and each is a real way single sign-on is got
wrong:

- **The signature.** Against the certificate you configured, never against a
  certificate carried in the document. A response signed by some other key that
  ships its own certificate is internally consistent and is refused.
- **What was actually signed.** The assertion is read back out of the exact
  bytes the signature covered, so a document carrying a second, forged assertion
  cannot make the verifier and the reader disagree. Signature wrapping is the
  most common way a SAML implementation that *does* check signatures is still
  broken.
- **The algorithm.** RSA or ECDSA with SHA-256 or better. SHA-1 is refused, and
  so is any HMAC: an HMAC would let anybody holding a shared secret forge an
  assertion.
- **The audience**, against the entity ID above. An assertion your provider
  issued for a different service is a valid signature over somebody else's
  login.
- **The validity window**, with five minutes of clock tolerance in both
  directions. Configurable per connection.
- **The recipient and `InResponseTo`.** A response answering a request nobody
  made is refused, and so is one answering a different request.
- **Replay.** Each assertion identifier is remembered until it expires, using a
  unique constraint rather than a read followed by a write, so two requests
  racing with the same assertion cannot both get through.

## Connecting OIDC

```
Redirect URI   https://<your-control-plane>/sso/oidc/<handle>/callback
```

You supply the issuer, the client ID and the client secret. The endpoints are
read from the provider's discovery document when the connection is configured,
not on every login: discovery is a network call to somebody else's service, and
putting it on the critical path of every sign-in makes their brief outage your
sign-in outage.

PKCE is always used, even though this is a confidential client that holds a
secret. The secret stops somebody else redeeming a stolen authorization code
from their own server; it does nothing about somebody feeding a stolen code into
our callback. The verifier does.

`state` and `nonce` are separate values doing separate jobs and both are
required: `state` is round-tripped through the browser and consumed once,
`nonce` comes back inside the signed token and binds it to this login rather
than to some other login at the same provider.

The `alg: none` and algorithm-confusion attacks are both refused by an
allow-list that contains no HMAC algorithm at all, so there is no code path in
which the provider's published public key could be used as a shared secret.

## Claiming a domain

Add the domain, then create the TXT record you are shown:

```
_antifailure-verification.<your-domain>   TXT   <token>
```

Until it is verified, the domain routes nobody and an assertion naming an
address in it is refused with `AF-EE-SSO-002`. A verified claim is exclusive; an
unverified one is not, so a typo in another organisation cannot stop you
claiming your own domain.

Once verified, `/sso/start?email=someone@your-domain` sends the browser to your
provider. That endpoint reveals that a domain uses single sign-on and which
connection handles it, which is the same fact the redirect itself announces. It
reveals nothing about any other domain and nothing you have not verified.

## Roles from groups

Map a group claim to a role and it is applied on every sign-in, so removing
somebody from a group in your directory takes effect at their next login rather
than never. Where several groups map, the most privileged wins: taking the first
match makes the result depend on the order your provider happened to send the
claims.

A role set by hand here is not overwritten by the directory. Somebody promoted
in this product stays promoted.

Just-in-time provisioning respects your seat count. When the seats are full the
addition is refused with `AF-EE-004` and **nothing is removed**. A product that
made room by evicting somebody would be turning a billing question into an
outage for a person who did nothing.

## Requiring single sign-on

Turn enforcement on and GitHub sign-in stops being a way into your
organisation. Someone signing in with GitHub is still signed in, and lands with
no organisation rather than being refused outright, which matters for the next
section.

Enforcement can only be turned on for a connection that is already enabled, and
turning it on issues **ten recovery codes, shown once**. They are not a separate
step you can skip: an organisation that has required single sign-on and has no
way back in is a support incident with no self-service fix, and the failure is
one bad metadata paste away.

Only hashes are stored. If you lose the codes, turn enforcement off and on again
from a session that still works.

### Break-glass

If your provider is misconfigured or down, an **owner** can get back in:

1. Sign in with GitHub. You land signed in with no organisation.
2. `POST /sso/break-glass` with the organisation and one recovery code.

The code is spent, cannot be used again, and a `sso.break_glass.used` entry is
written to the audit log with the address and user agent. Only owners: a member
with a recovery code could walk around enforcement for themselves, which is most
of what enforcement is for.

Note what this is not. It is not a second way to authenticate. There is no
unauthenticated lookup keyed on a recovery code anywhere in this feature. It is
a decision not to apply enforcement to a sign-in that has already happened.

Existing sessions are honoured until they expire, so turning enforcement on does
not sign everybody out mid-work.

## Configuration

| Variable | What it is |
| --- | --- |
| `AF_EE_SSO_KEY` | 32 bytes, base64, encrypting the OIDC client secret and the service provider private key at rest. Generate with `openssl rand -base64 32`. The control plane refuses to start without it. |

Secrets are sealed with AES-256-GCM under that key, with the organisation ID
authenticated as additional data. That last part is not decoration: without it a
ciphertext is portable, and anybody able to write a row could copy another
tenant's encrypted client secret into their own connection and have the server
decrypt it for them.

## Testing against a real provider

Everything above is exercised by suites that build their own assertions and
their own tokens. That proves the verifier refuses what it should, and it does
not prove interoperability, because a fixture written by the same person who
wrote the parser agrees with the parser by construction. The things that break
against a real provider are the ones nobody thought to put in a fixture: the
namespace prefix it happens to use, where it puts the signature, whether it
sends the address as a NameID or a claim.

So there is a conformance suite that drives a real Keycloak, and a script that
boots one:

```
eval "$(ee/web/sso/test/keycloak-up.sh)"
cd ee/web/sso && node --test test/keycloak.test.ts
ee/web/sso/test/keycloak-up.sh --down
```

The `eval` is required rather than tidy. The script generates a certificate at
run time into a temporary directory outside the repository, and prints both
`AF_KEYCLOAK_URL` and the `NODE_EXTRA_CA_CERTS` that names a file which did not
exist until it ran.

The provider has to be HTTPS. This is not a preference: `parseIdentityProviderMetadata`
refuses an `http` single sign-on URL and `discover` refuses an `http` token
endpoint, because a token exchange over plain HTTP carries a client secret in
clear text. An earlier version of this suite documented a plain HTTP provider
and therefore could not have passed, which is worth recording because a suite
gated behind an environment variable is a suite nobody runs, and a suite nobody
runs is a claim nobody checks.

The suite is deliberately not part of `just gate` or CI: it boots a container
and takes minutes. Keycloak is also not a substitute for Entra ID or Okta, which
have their own quirks, and `docs/plan/STATUS.md` is explicit about which of the
three any given row rests on.

## What is not here yet

- **Encrypted assertions.** Signed assertions over TLS are supported; XML
  encryption of the assertion body is not. If your provider requires it, say so.
- **Back-channel logout.** Signing out here does not sign you out of your
  provider.
- **Signed AuthnRequests** are implemented but the key has to be supplied
  directly; there is no UI for generating one yet.

Each of those is absent rather than half-present, and none of them is claimed
anywhere in the product.
