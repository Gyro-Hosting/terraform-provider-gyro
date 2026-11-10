resource "gyro_dns_record" "www" {
  service_id = gyro_dns_zone.example.service_id
  zone       = gyro_dns_zone.example.name
  name       = "www"
  type       = "A"
  ttl        = 300
  records    = [gyro_server.web.ipv4]
}

resource "gyro_dns_record" "mx" {
  service_id = gyro_dns_zone.example.service_id
  zone       = gyro_dns_zone.example.name
  name       = "@"
  type       = "MX"
  records    = ["10 mx1.example.com.", "20 mx2.example.com."]
}
