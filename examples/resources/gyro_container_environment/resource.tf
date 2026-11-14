resource "gyro_container_environment" "app" {
  plan                  = "pro" # GET /cloud/containers/plans
  name                  = "Gyro Project"
  extra_nodes           = 1  # same price as a plan node, billed hourly
  egress_approval_hours = 24 # new egress IPs wait for your approval (0 = off)

  lifecycle { prevent_destroy = true } # destroy deletes the environment and its data
}

output "pending_egress_ips" {
  value = gyro_container_environment.app.pending_egress_ips
}
