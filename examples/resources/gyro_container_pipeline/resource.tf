# Build the repository's Dockerfile on every push to main (or a v* tag) and roll the "web" service out to it.
resource "gyro_container_pipeline" "web" {
  service_id = 12345 # container environment (gyro-ecs-1)
  name       = "web"
  repo       = "https://github.com/gyro-ornek/uygulama"
  token      = var.github_read_token # private repository only (read access)
  branch     = "main"
  tags       = "v*"
  image      = "web"
  targets    = ["web"]

  # optional build step before the Dockerfile (tests ...); secrets are environment variables here
  build_image = "node:22-alpine"
  commands    = ["npm ci", "npm test"]
  secrets     = { NPM_TOKEN = var.npm_token }
}

# Add these to the repository: Settings > Webhooks (application/json, push event)
output "webhook_url" { value = gyro_container_pipeline.web.webhook_url }
output "webhook_secret" {
  value     = gyro_container_pipeline.web.webhook_secret
  sensitive = true
}

# Private repository over SSH: Gyro creates a read-only deploy key (the private key never leaves Gyro).
# GitHub / GitLab / Bitbucket host keys are pinned; for your own git server set ssh_host_fingerprint.
resource "gyro_container_pipeline" "api" {
  service_id = 12345
  name       = "api"
  repo       = "git@github.com:gyro-ornek/api.git"
  deploy_key = true
  image      = "api"
  targets    = ["api"]
}

# Add this to the repository: Settings > Deploy keys (read only)
output "deploy_key" { value = gyro_container_pipeline.api.deploy_key_public }

# Archive builds only (no repository): upload with the API (POST .../pipelines/{id}/uploads) and run with the upload id.
resource "gyro_container_pipeline" "site" {
  service_id   = 12345
  name         = "site"
  git_provider = "archive"
  image        = "site"
  targets      = ["web"]
}
