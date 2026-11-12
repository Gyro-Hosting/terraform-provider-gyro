resource "gyro_server_ip" "extra" {
  count     = 2
  server_id = gyro_server.web.id
}

output "extra_ips" {
  value = gyro_server_ip.extra[*].ip
}
