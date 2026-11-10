# Web server: 80/443 open to everyone, SSH only from the office, everything else denied.
resource "gyro_firewall" "web" {
  server_id      = 1234
  default_action = "deny"

  rule {
    action      = "allow"
    protocol    = "tcp"
    port        = "80,443"
    description = "web"
  }
  rule {
    action   = "allow"
    protocol = "udp"
    port     = "443"
  }
  rule {
    action = "allow"
    port   = "22"
    source = "203.0.113.10"
  }
}

# Database: open, but 3306 closed to one address; outbound SMTP blocked.
resource "gyro_firewall" "db" {
  server_id      = 1235
  default_action = "allow"

  rule {
    action = "deny"
    port   = "3306"
    source = "198.51.100.7"
  }
  rule {
    direction = "out"
    action    = "deny"
    port      = "25"
  }
}
