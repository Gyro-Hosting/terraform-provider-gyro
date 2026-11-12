# Alert channels of a Gyro Containers (ECS) environment.
resource "gyro_container_notifications" "alerts" {
  service_id  = 123
  email       = true
  sms         = false
  telegram    = "123456789" # send /start to @gyrouptime_bot, it replies with your chat id
  webhook_url = "https://ops.example.com/gyro-alerts"
  events      = ["down", "crashloop", "replicas_low", "scale_max", "capacity"]
  cooldown    = 30
}

# Verify webhooks: X-Gyro-Signature = "sha256=" + HMAC-SHA256(secret, X-Gyro-Timestamp + "." + body)
output "webhook_secret" {
  value     = gyro_container_notifications.alerts.webhook_secret
  sensitive = true
}
