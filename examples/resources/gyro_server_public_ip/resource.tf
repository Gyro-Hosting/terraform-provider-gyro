# Private-only database server: reachable from the VPC (and the web console), internet egress through the VPC gateway NAT.
resource "gyro_server_nic" "db" {
  server_id = gyro_server.db.id
  subnet_id = gyro_vpc.uretim.default_subnet_id
}

resource "gyro_server_public_ip" "db" {
  server_id  = gyro_server.db.id
  enabled    = false
  depends_on = [gyro_server_nic.db]
}
