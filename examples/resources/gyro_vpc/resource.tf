# Private network for Gyro Cloud Istanbul (tr-ist-1) servers. The VPC opens with a "default" /24 subnet.
resource "gyro_vpc" "uretim" {
  name = "uretim"
  cidr = "10.10.0.0/16"

  # Optional VPC gateway (NAT + port forwarding): hourly prepaid billing, 4.99 EUR/month cap.
  gateway_enabled = true
  inter_subnet    = "allow"
}

output "gateway_ip" {
  value = gyro_vpc.uretim.gateway_public_ip
}
