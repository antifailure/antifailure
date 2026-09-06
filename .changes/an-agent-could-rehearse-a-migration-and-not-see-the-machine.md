# added

An agent could rehearse a migration and could not find out whether the machine
could run one.

`af mcp` served four tools against eighty one commands. An agent could ask what
a migration would do and what the environment reached, and had no way to ask
the questions that come before either: is there a container daemon, is there a
golden this project can branch, is anything running, did the application send
the verification email, is a model key configured. Every one of those had to be
answered by a person at a terminal, and the usual way an agent found out was a
rehearsal that failed twenty minutes in for a reason that had nothing to do with
the code.

Thirteen more tools, named for the question rather than the command.
`check_prerequisites` is the one to call first: it runs `af doctor` and
`af runner check` and reports ready, blocked or undetermined, where undetermined
means a deciding question could not be answered and is never reported as a pass.
`inspect_environments`, `inspect_goldens`, `describe_model_key`,
`describe_control_plane_account`, `read_captured_messages` and
`list_webhook_events` read state and change nothing.
`extend_environment_lifetime`, `verify_model_key`, `send_webhook_event` and
`prepare_golden` change something and say what.

The three that DESTROY something say so on the wire. `destructiveHint` was
published as false on every tool, with a comment saying nothing here destroys
anything a caller owns, which was true while every tool made a throwaway
environment and removed it again. `remove_expired_environments` and
`remove_old_goldens` break that, so the hint is now declared per tool. Both plan
by default and remove nothing; carrying a plan out means naming every
environment or version the plan listed, and a set that has changed since is
refused rather than swept. Neither accepts a wildcard.

Nothing here reads, returns, stores or removes a credential. There is no tool
for `af secret`, `af token`, `af login`, `af provider set` or `af model set`, and
no argument anywhere that carries a key: what a result carries is a fingerprint,
a last four, or a token prefix. A support bundle is not offered as a tool
either, because it collects the application's own logs and every outbound
request it made. Free form text on its way into a result now passes the
engine's redactor as well, so a provider quoting back the key it just rejected
does not put that key in a model's context.
