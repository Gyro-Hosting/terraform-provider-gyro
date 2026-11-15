resource "gyro_container_logs" "app" {
  service_id     = gyro_container_environment.app.id
  retention_days = 90 # 7 (free), 30, 90, 365
  gb_month       = 10 # free: 5; paid: 10, 50, 100, 300

  rules = [
    { name = "payment errors", service = "api", pattern = "/api/payment", threshold = 20, window_minutes = 5 },
    { name = "any error", level = "error", threshold = 200, window_minutes = 10 },
  ]
}
