# Attach a tr-ist-1 server to the "app" subnet (next free address)
resource "gyro_server_nic" "web" {
  server_id = gyro_server.web.id
  subnet_id = gyro_subnet.app.subnet_id
}

# Fixed private address in the subnet created with the VPC
resource "gyro_server_nic" "db" {
  server_id = gyro_server.db.id
  subnet_id = gyro_vpc.uretim.default_subnet_id
  ip        = "10.10.0.20"
}
