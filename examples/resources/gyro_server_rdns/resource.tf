resource "gyro_server_rdns" "mail" {
  server_id = gyro_server.web.id
  ip        = gyro_server.web.ipv4
  hostname  = "mail.example.com"
}
