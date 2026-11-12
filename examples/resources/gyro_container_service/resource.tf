# A web service in your Gyro Containers (ECS) environment (Istanbul).
# service_id is the id of the container environment you ordered (see gyro_services).
variable "db_password" {
  type      = string
  sensitive = true
}

resource "gyro_container_service" "web" {
  service_id  = 123
  name        = "web"
  image       = "ghcr.io/acme/web:1.4"
  port        = 8080
  cpu_m       = 500
  memory_mi   = 512
  public      = true
  health_path = "/healthz"

  env     = { MODE = "prod" }
  secrets = { DB_PASSWORD = var.db_password } # write only, never read back

  # Replicas follow the load: 2..6, target 70 % CPU of cpu_m (replicas is ignored while autoscaling is set).
  autoscaling = {
    min         = 2
    max         = 6
    cpu_percent = 70
  }
  # Alert when fewer than 2 replicas are ready (channels: gyro_container_notifications).
  notification = {
    min_ready = 2
  }

  # Point a CNAME from your domain to gyro_container_service.web.host first.
  domains = ["app.example.com"]
}

output "web_url" {
  value = gyro_container_service.web.url
}
