# added

`af token create`, `af token list` and `af token rm` mint, list and revoke the
engine tokens a CI job or a self-hosted engine presents as
`AF_CONTROL_PLANE_TOKEN`. Three places told people to create one in the control
plane and nothing anywhere could: the console has no page for them and the API
could only list and revoke, so an engine could authenticate against a row that
had no producer outside the test fixtures.

`af-control-plane-backup create-org` creates the first organization on a control
plane nobody has installed the GitHub App on yet. A tenant otherwise begins only
when an installation arrives, so a self-hoster's every sign-in landed with no
organization and nothing in the console could be reached. It creates no account
and grants no role: sign in through GitHub, then use break-glass as before.
