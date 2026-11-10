# Two cloud servers behind a managed load balancer (EU regions: eu-de-1, eu-de-2, eu-fi-1).
resource "gyro_server" "web" {
  count         = 2
  plan          = "standart-2-4"
  region        = "eu-de-1"
  image         = "ubuntu-24.04"
  hostname      = "web-${count.index + 1}.example.com"
  confirm_order = true
}

resource "gyro_load_balancer" "web" {
  size          = "small" # small | medium | large (GET /cloud/load-balancer-types)
  region        = "eu-de-1"
  algorithm     = "least_connections"
  confirm_order = true # creating a load balancer is a paid order from your account balance

  services = [
    {
      protocol         = "http"
      listen_port      = 80
      destination_port = 80
      sticky_sessions  = true
      health_check     = { protocol = "http", path = "/healthz", interval = 10, timeout = 5, retries = 3 }
    },
    {
      # TLS passthrough: certificates stay on the servers
      protocol         = "tcp"
      listen_port      = 443
      destination_port = 443
    },
  ]

  targets = [for s in gyro_server.web : { type = "server", server_id = tonumber(s.id) }]
}

output "load_balancer_ip" {
  value = gyro_load_balancer.web.ipv4 # point your A record here
}
