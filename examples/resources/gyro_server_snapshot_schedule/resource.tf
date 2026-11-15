# Every night at 03:30 (Türkiye time), keep the last 3 automatic snapshots.
resource "gyro_server_snapshot_schedule" "db" {
  server_id = tonumber(gyro_server.db.id)
  hour      = 3
  minute    = 30
  keep      = 3
}

# Weekly on Sunday 02:00, keep 4 (about a month).
resource "gyro_server_snapshot_schedule" "web" {
  server_id = tonumber(gyro_server.web.id)
  frequency = "weekly"
  weekday   = "sunday"
  hour      = 2
  keep      = 4
}
