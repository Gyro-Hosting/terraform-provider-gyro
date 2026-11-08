resource "gyro_domain_record" "www" {
  domain = "example.com"
  type   = "A"
  name   = "www"
  value  = gyro_server.web.ipv4
  ttl    = 3600
}
