# HTTPS on the gateway's public IP -> private web server (needs gateway_enabled = true on the VPC)
resource "gyro_port_forward" "https" {
  vpc_id      = gyro_vpc.uretim.id
  protocol    = "tcp"
  public_port = 443
  target_ip   = gyro_server_nic.web.ip
  target_port = 443
}
