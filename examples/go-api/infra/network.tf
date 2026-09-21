// What this service may reach, and what may reach it.
//
// The egress rule in antifailure.yaml says api.stripe.com and nothing else, and
// it is the thing a rehearsal actually enforces: every outbound request in a
// run meets that policy and gets a decision, which `af net log` prints. These
// resources are the production half of the same sentence, so a change to one of
// them selects the egress check and the run reports what the policy would do.
//
// The two are not kept in step by anything. A rule opened here and not named in
// the manifest is a call production can make and a rehearsal refuses, which is
// the more useful way round: the run says no before the merge rather than after.

resource "aws_security_group" "api" {
  name        = "${local.name}-api"
  description = "The orders API. Outbound to the payment provider only."
  tags        = local.tags
}

resource "aws_security_group_rule" "payments" {
  security_group_id = aws_security_group.api.id
  description       = "Payment intents, to the provider the manifest names."

  type        = "egress"
  protocol    = "tcp"
  from_port   = 443
  to_port     = 443
  cidr_blocks = ["0.0.0.0/0"]
}

resource "aws_security_group_rule" "database" {
  security_group_id = aws_security_group.database.id
  description       = "Postgres, from the API and from nothing else."

  type                     = "ingress"
  protocol                 = "tcp"
  from_port                = 5432
  to_port                  = 5432
  source_security_group_id = aws_security_group.api.id
}

resource "aws_security_group" "database" {
  name        = "${local.name}-database"
  description = "The orders database. Reachable from the API only."
  tags        = local.tags
}
