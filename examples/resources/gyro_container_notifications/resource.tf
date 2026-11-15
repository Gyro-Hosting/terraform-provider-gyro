# Alert channels of a Gyro Containers (ECS) environment.
resource "gyro_container_notifications" "alerts" {
  service_id  = 123
  email       = true
  sms         = false
  telegram    = "123456789" # send /start to @gyrouptime_bot, it replies with your chat id
  webhook_url = "https://ops.example.com/gyro-alerts"
  # events: the FULL set to enable; omit it to keep the selection on the platform (all on by default)
  events   = ["down", "crashloop", "replicas_low", "scale_max", "capacity", "egress", "job_failed", "schedule_failed", "schedule_missed"]
  cooldown = 30
}

# Verify webhooks: X-Gyro-Signature = "sha256=" + HMAC-SHA256(secret, X-Gyro-Timestamp + "." + body)
output "webhook_secret" {
  value     = gyro_container_notifications.alerts.webhook_secret
  sensitive = true
}
