# A container environment inside the VPC: the containers reach the VPC servers from a fixed /28 (source_range),
# and the VPC servers reach the published service at endpoint_ip:8080. The subnet must be /26 or larger.
# Free for node environments; for a serverless environment the connection costs 2.99 EUR/month, billed hourly while
# attached (monthly_price_eur) - applying accepts the charge, destroying stops it.
resource "gyro_container_vpc_attachment" "app" {
  environment_id = gyro_container_environment.gyro_proje.id
  vpc_id         = gyro_vpc.uretim.id
  subnet_id      = gyro_vpc.uretim.default_subnet_id

  expose {
    service = "api"
    port    = 8080
  }
}

# allow the containers in a server firewall once; the range does not change when nodes are replaced
output "containers_source_range" {
  value = gyro_container_vpc_attachment.app.source_range
}

output "api_from_the_vpc" {
  value = "http://${gyro_container_vpc_attachment.app.endpoint_ip}:8080"
}
