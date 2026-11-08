resource "gyro_webhook" "ops" {
  url    = "https://hooks.example.com/gyro"
  events = ["service.active", "service.suspended", "invoice.paid"]
}

output "webhook_secret" {
  value     = gyro_webhook.ops.secret
  sensitive = true
}
