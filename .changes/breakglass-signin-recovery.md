# added

The first member of an organization becomes its owner when GitHub confirms they
administer it. Every organization here is created by an installation webhook
before anybody signs in, and sign-in mapped a GitHub administrator to `admin`,
so no organization had an owner and nothing held `billing.manage`. GitHub still
has to say `admin`: a first sign-in during an outage is still a member, because
guessing upward would hand out a tenant on a timeout.

`af-control-plane-backup break-glass` sets a role directly in the database, for
when the GitHub App is gone and nobody inside the organization can act. It
writes a `member.break_glass` audit entry carrying the reason, it refuses to
leave an organization with no owner, it cannot create an account, and it grants
no session. `--dry-run` reports what would change and writes nothing. The
runbook has it under "Nobody can sign in".
