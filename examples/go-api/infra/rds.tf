// The database, and the two things about it a rehearsal can actually speak to.
//
// engine_version is the one that matters most and is the easiest to get wrong.
// antifailure.yaml beside this file declares `database: version: 17`, and that
// is the version the migration rehearsal runs this change's migrations against.
// The two numbers are a pair, in two files, and nothing used to hold both: move
// production to 18 here and the rehearsal quietly keeps proving your migrations
// against 17. `af change` now says so, naming both numbers.
//
// The parameter group is the second one. lock_timeout decides whether a
// migration waits behind an open transaction or gives up, which is the
// difference between a deploy and an outage, and the migration rehearsal is the
// thing that measures it.

resource "aws_db_parameter_group" "primary" {
  name   = "${local.name}-pg17"
  family = "postgres17"
  tags   = local.tags

  parameter {
    name  = "lock_timeout"
    value = "5000"
  }

  parameter {
    name  = "idle_in_transaction_session_timeout"
    value = "60000"
  }
}

resource "aws_db_instance" "primary" {
  identifier = local.name
  tags       = local.tags

  engine         = "postgres"
  engine_version = "17.2"

  instance_class    = "db.t4g.medium"
  allocated_storage = 100

  parameter_group_name = aws_db_parameter_group.primary.name

  db_name  = "orders"
  username = "orders"

  backup_retention_period = 7
  deletion_protection     = true
  skip_final_snapshot     = false
}
