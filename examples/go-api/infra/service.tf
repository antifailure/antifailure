// How much of the service there is.
//
// desired_count and the size of one task are what a load result is a statement
// about, so a change to either selects the load check: the question "did this
// get slower" has a different answer at two copies than at six.
//
// The numbers are deliberately the same ones the manifest's `resources` block
// names for a single instance, half a core and half a gigabyte, so that the two
// files describe the same service rather than two different ones.

resource "aws_ecs_service" "api" {
  name            = local.name
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.api.arn
  desired_count   = 4
  tags            = local.tags
}

resource "aws_ecs_cluster" "main" {
  name = "${local.name}-cluster"
  tags = local.tags
}

resource "aws_ecs_task_definition" "api" {
  family = local.name
  tags   = local.tags

  cpu    = "512"
  memory = "512"

  container_definitions = jsonencode([
    {
      name      = "api"
      image     = "ghcr.io/example/orders-api:latest"
      essential = true
      portMappings = [
        {
          containerPort = 3000
        }
      ]
    }
  ])
}

resource "aws_appautoscaling_target" "api" {
  resource_id        = "service/${aws_ecs_cluster.main.name}/${aws_ecs_service.api.name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"

  min_capacity = 4
  max_capacity = 20
}
