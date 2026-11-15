# HTTPS on the gateway's public IP -> private web server (needs gateway_enabled = true on the VPC)
resource "gyro_port_forward" "https" {
  vpc_id      = gyro_vpc.uretim.id
  protocol    = "tcp"
  public_port = 443
  target_ip   = gyro_server_nic.web.ip
  target_port = 443
}

# SSH only from the office and the VPN (source allow list, changed in place; empty = everyone)
resource "gyro_port_forward" "ssh" {
  vpc_id       = gyro_vpc.uretim.id
  protocol     = "tcp"
  public_port  = 2222
  target_ip    = gyro_server_nic.web.ip
  target_port  = 22
  source_cidrs = ["203.0.113.10/32", "198.51.100.0/24"]
  name         = "ssh-admin" # label only; renamed in place
}
