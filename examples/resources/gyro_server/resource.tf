data "gyro_products" "small" {
  location       = "fsn1"
  server_type    = "cx23"
  available_only = true
}

resource "gyro_server" "web" {
  product_id    = data.gyro_products.small.products[0].id
  image         = "debian-12"
  hostname      = "web-1.example.com"
  ssh_key_ids   = [tonumber(gyro_ssh_key.laptop.id)]
  power_state   = "running"
  confirm_order = true # creating a server is a paid order from your account balance
}

output "web_ipv4" {
  value = gyro_server.web.ipv4
}
