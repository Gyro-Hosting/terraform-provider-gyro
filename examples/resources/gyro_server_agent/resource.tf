# Keep the Gyro guest agent current (needed for gyro_server_nic / gyro_server_public_ip on servers installed before 14 Nov 2026).
resource "gyro_server_agent" "db" {
  server_id = tonumber(gyro_server.db.id)
}
