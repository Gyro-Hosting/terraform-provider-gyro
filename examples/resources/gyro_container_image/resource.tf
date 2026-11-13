# Copy an image into the environment's private Gyro registry (no docker push), then run it.
resource "gyro_container_image" "web" {
  service_id = 12345 # container environment (gyro-ecs-1)
  source     = "nginxinc/nginx-unprivileged:1.27-alpine"
  repo       = "web"
  tag        = "1.27"
}

# A private image: the token is used only for the copy (prefer a read-only token).
resource "gyro_container_image" "app" {
  service_id = 12345
  source     = "ghcr.io/gyro-ornek/uygulama:1.0"
  username   = "gyro-ornek"
  password   = var.ghcr_read_token
}

resource "gyro_container_service" "web" {
  service_id = 12345
  name       = "web"
  image      = gyro_container_image.web.image
  port       = 8080
  public     = true
}
