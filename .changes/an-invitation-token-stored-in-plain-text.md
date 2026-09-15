# security

A raw invitation token was stored in plain text, beside the hash that exists so
that it is not.

An invitation is kept as a sha256 and nothing else, so a copy of the table is
not a list of working invitations. The console asked to be returned to
`/invite?token=<the raw token>` after sign-in, and the control plane stores a
return target verbatim: `oauth_states.redirect_to` for the GitHub handshake, and
`email_signin_tokens.redirect_to` for a sign-in link. Both are plain text. So
the token sat readable in the database for the ten minutes a handshake lives,
and for up to a day when nobody came back to redeem it, which is the case where
the token is still valid.

A return target now carries no `token` parameter, dropped where both writers and
every other caller pass through, rather than at either writer. The console keeps
the token in the tab it already lives in for the trip to the identity provider
and back, so an invitation still opens, is still accepted, and the token still
reaches only the person who was sent it.
