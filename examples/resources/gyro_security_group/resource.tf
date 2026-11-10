# Reusable allow lists (AWS security group model). Stateful: replies to allowed connections pass automatically.
resource "gyro_security_group" "web" {
  name        = "web"
  description = "HTTP/HTTPS for everyone"

  rule {
    type = "http"
  }
  rule {
    type = "https"
  }
}

resource "gyro_security_group" "admin" {
  name = "admin"

  rule {
    type        = "ssh"
    source      = ["203.0.113.10/32"]
    description = "office"
  }
  rule {
    type   = "custom_tcp"
    port   = "8000-8100"
    source = ["198.51.100.0/24"]
  }
  # outbound: a group without outbound rules does not restrict outbound traffic
  rule {
    direction = "out"
    type      = "all"
  }
}
