# Pick a product by plan + region (see gyro_products / gyro_locations) ...
resource "gyro_server" "web" {
  plan          = "standart-2-4"
  region        = "eu-de-1"
  image         = "debian-12"
  hostname      = "web-1.example.com"
  ssh_key_ids   = [tonumber(gyro_ssh_key.laptop.id)]
  power_state   = "running"
  confirm_order = true # creating a server is a paid order from your account balance
}

# ... or by product id from the catalog, e.g. the first product in stock in Helsinki.
data "gyro_products" "helsinki" {
  region         = "eu-fi-1"
  available_only = true
}

resource "gyro_server" "db" {
  product_id    = data.gyro_products.helsinki.products[0].id
  image         = "ubuntu-24.04"
  confirm_order = true
}

output "web_ipv4" {
  value = gyro_server.web.ipv4
}
