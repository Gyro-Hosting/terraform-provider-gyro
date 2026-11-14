# Look up a VPC by name (or by id)
data "gyro_vpc" "uretim" {
  name = "uretim"
}

resource "gyro_server_nic" "worker" {
  server_id = gyro_server.worker.id
  subnet_id = data.gyro_vpc.uretim.default_subnet_id
}
