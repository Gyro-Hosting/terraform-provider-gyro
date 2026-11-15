# Every night at 03:00 Istanbul time: run a command with the image, variables and secrets of the "api" service.
resource "gyro_container_schedule" "report" {
  environment_id = 123
  name           = "nightly-report"
  cron           = "0 3 * * *"
  timezone       = "Europe/Istanbul"
  service        = "api"
  command        = ["npm", "run", "report"]
  retries        = 2
}

# Every 15 minutes: call an endpoint of your application (headers are kept secret by the API).
resource "gyro_container_schedule" "queue" {
  environment_id = 123
  name           = "queue-tick"
  rate_minutes   = 15
  url            = "https://api.example.com/cron"
  method         = "POST"
  headers        = { Authorization = "Bearer ${var.cron_token}" }
}

variable "cron_token" {
  type      = string
  sensitive = true
}
